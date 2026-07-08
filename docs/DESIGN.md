# DESIGN.md — chat-machines Architecture

## Overview

chat-machines is a Go application that manages multi-AI chat rooms. Each AI participant runs as an opencode-serve session. The application orchestrates round-robin message exchange between participants and exposes an MCP server for external control.

## Component Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                      chat-machines                          │
│                                                             │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌─────────┐  │
│  │ MCP      │   │ Room     │   │ Chat     │   │ Agent   │  │
│  │ Server   │──▶│ Store    │──▶│ Engine   │──▶│ Client  │  │
│  │          │◀──│          │◀──│          │◀──│         │  │
│  └──────────┘   └──────────┘   └──────────┘   └────┬────┘  │
│       ▲                                           │      │
│       │ chat_send, chat_join, chat_leave          │      │
└───────┼───────────────────────────────────────────┼──────┘
        │                                             │
        │ Human adjunct (Kirill/Baron session)        │ HTTP POST/GET
        │                                             │
┌───────┴─────────────────────────────────────────┐   │
│                   opencode-serve                 │   │
│                                          :7709   │   │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐    │   │
│  │ Session  │   │ Session  │   │ Session  │ ...  │   │
│  │ Baron    │   │ Manager  │   │ Agent N  │      │   │
│  └──────────┘   └──────────┘   └──────────┘      │   │
│  ┌──────────┐                                    │   │
│  │ Session  │ ◀── human adjunct                   │   │
│  │ Kirill   │                                    │   │
│  └──────────┘                                    │   │
└──────────────────────────────────────────────────┘   │
                                                       │
┌──────────────────────────────────────────────────────┘
│                    memory-store-mcp (:7708)
│  ┌──────────────────────────────────────────┐
│  │ memory/project/chat-machines/digest/{id}  │
│  │  (caller saves digest returned by chat_stop) │
│  └──────────────────────────────────────────┘
└──────────────────────────────────────────────┘
```

## Directory Layout

```
chat-machines/
├── main.go              # Entrypoint: flags (including --auto-save), MCP server startup, engine init
├── server.go            # MCP server setup, tool registration, system instructions
├── agent.go             # OpenCodeServeClient: HTTP client for opencode-serve
├── room.go              # Room, Participant, Message, RoomStore, RoomSnapshot, adjunct state
├── engine.go            # ChatEngine: round-robin loop, goroutine per room, digest storage
├── summary.go           # Digest builder and optional memory-store-mcp saver
├── store.go             # Optional SQLite persistence (enabled by --db flag)
├── agent_test.go        # Tests for agent client
├── room_test.go         # Tests for room store
├── engine_test.go       # Tests for chat engine and OK protocol
├── go.mod / go.sum
├── README.md
└── docs/
    ├── CONTEXT.md
    ├── STATUS.md
    └── DESIGN.md
```

## Key Design Decisions

### 1. Flat Layout (not internal/)

Following `memory-store-mcp` pattern: flat root layout with `package main`. All `.go` files are at module root.

**Rationale:** Simplicity. There's only one binary. If a CLI client is added later, it goes under `cmd/chat-cli/`.

### 2. In-Memory State (no database required)

Rooms, participants, and messages are stored in memory (`map[string]*Room` with `sync.RWMutex`). SQLite is optional (`--db` flag) for debugging.

**Rationale:** Chat rooms are ephemeral. After completion, only the summary is persisted (to memory-store-mcp). Full message history in SQLite is a debug feature.

### 2a. Room Snapshots for Safe Concurrent Reads

MCP handlers and tests never read mutable `*Room` fields directly. `RoomStore` exposes `GetSnapshot(id)` and `ListSnapshots()`, which return immutable `RoomSnapshot` copies taken under the store lock.

**Rationale:** The room goroutine writes to `Messages`, `CurrentRound`, and `Status` while MCP handlers read them. Snapshots eliminate this entire class of races without scattering locks in handler code.

### 2b. Global SQLite Logging

Message logging to SQLite is enabled globally by the `--db` flag at server startup. There is no per-room `save_log` toggle: a single server either logs all rooms or none.

**Rationale:** Avoids unused MCP parameters, keeps `chat_create` focused on chat configuration, and matches the current implementation where `ChatEngine` owns an optional `logger`.

### 3. Goroutine per Room

Each active room runs in its own goroutine via `ChatEngine.run()`. The engine manages a `map[string]context.CancelFunc` for stopping individual rooms.

**Rationale:** Isolated lifecycle. Stopping one room cancels its context without affecting others.

### 4. opencode-serve HTTP API (not CLI)

Agent sessions are managed via HTTP API (`POST /session`, `POST /session/{id}/message`), not via `opencode run` subprocess.

**Rationale:** 
- Faster: no model loading latency per message
- Session persistence: sessions remain active across rounds
- Async support: `prompt_async` enables fire-and-forget message sending
- Proven pattern: alice-baron uses this for voice assistant

### 5. OK-Protocol for 3+ Participants

When >2 participants:
- Each participant decides whether to contribute or say "OK"
- Two consecutive rounds where ALL say "OK" → auto-stop
- This prevents endless chats where nobody has anything new to say

**Rationale:** In 1-on-1 chats, every message deserves a response. In groups, not everyone needs to speak every round.

### 6. Summary Collection at Chat End

When a room stops (via `chat_stop`, max rounds, or auto-stop), the engine waits synchronously for cleanup:

1. The room goroutine finishes any in-progress round.
2. Each participant is asked to write a summary.
3. All summaries are collected and a unified digest is stored on the room (`Room.StoredDigest`).
4. The room goroutine signals completion.
5. `chat_stop` returns only after the digest is stored, so its response always contains the digest.
6. `chat_get` includes the digest for stopped rooms.
7. If `--auto-save` is true and a `SummarySaver` is configured, the digest is also saved to memory-store-mcp.

**Rationale:** Returning the digest must be deterministic for the caller. The engine therefore waits for summary collection before `chat_stop` returns, and auto-save is an opt-in side effect controlled by the operator.

### 7. Human-in-Chat via Adjunct

Humans participate through their existing opencode session:
- `chat_join(room_id)` registers the human session as an adjunct on the room
- `chat_send(room_id, message)` injects a human message into the room transcript
- `chat_get(room_id)` is polled by Baron to relay new room messages back to the user
- `chat_leave(room_id)` removes the adjunct

The adjunct is stored as room metadata (`AdjunctSessionID`, `AdjunctName`), not as a round-robin participant. This lets the human speak at any time without breaking the AI turn order.

**Rationale:** No special "human session" needed; the human is an observer who can interject.

### 8. Agent Interface for Testability

`ChatEngine` depends on the `Agent` interface, not directly on `*AgentClient`:

```go
type Agent interface {
	CreateSession(name, agent, model, workDir string) (string, error)
	SendMessage(sessionID, text, agent, model string) (string, error)
	SendMessageAsync(sessionID, text, agent, model string) error
}
```

**Rationale:** Allows deterministic integration tests with a mock agent without requiring a real opencode-serve instance.

## Concurrency Model

```
main goroutine
  ├── MCP server goroutine (stdio or HTTP)
  │     └── handles incoming tool calls
  │           ├── chat_create → creates room → spawns room goroutine
  │           ├── chat_list   → reads RoomStore (RWMutex.RLock)
  │           ├── chat_get    → reads RoomStore (RWMutex.RLock)
  │           ├── chat_stop   → cancels room context
  │           └── chat_stats  → reads RoomStore (RWMutex.RLock)
  │
  ├── Room goroutine 1
  │     └── run()
  │           ├── send onboarding ──── HTTP to opencode-serve
  │           ├── runRound ────────── HTTP to all participants in parallel
  │           └── addMessage ──────── RoomStore (RWMutex.Lock)
  │
  ├── Room goroutine 2
  │     └── run() ...
  │
  └── Room goroutine N ...
```

## Configuration

```bash
chat-machines \
  --opencode-url http://127.0.0.1:7709 \  # opencode-serve address
  --http :8080 \                           # MCP HTTP transport (optional)
  --db /tmp/chat-machines.db \             # SQLite log (optional)
  --memory-mcp http://127.0.0.1:7708/mcp \ # memory-store-mcp address
  --auto-save \                            # auto-save digest to memory-store-mcp (default: false)
  --timeout 120s \                          # per-message timeout
  --log-dir /var/log/chat-machines          # file logging (optional)
```

## MCP Tool Reference

| Tool | Purpose |
|---|---|
| `chat_create` | Create and start a multi-AI chat room |
| `chat_list` | List active rooms |
| `chat_get` | Get room state (includes `digest` for stopped rooms) |
| `chat_send` | Inject a message from a human into the room |
| `chat_join` | Join a room as a human adjunct |
| `chat_leave` | Leave a room as a human adjunct |
| `chat_stop` | Stop a room and return the summary digest |
| `chat_stats` | Global chat statistics |

## Future Extensions (not in v1)

- **Scenarios/Templates:** Predefined chat patterns
- **WebSocket streaming:** Real-time transcript delivery
- **Agent marketplace:** Register/catalog available agents
- **Dream Weaver integration:** Scheduled nightly chats
