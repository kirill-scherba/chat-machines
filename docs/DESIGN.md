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
│                                                     │      │
└─────────────────────────────────────────────────────┼──────┘
                                                      │
                                              HTTP POST/GET
                                                      │
┌─────────────────────────────────────────────────────┼──────┐
│                   opencode-serve                     │      │
│                                              :7709  │      │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐        │      │
│  │ Session  │   │ Session  │   │ Session  │   ...   │      │
│  │ Baron    │   │ Manager  │   │ Agent N  │         │      │
│  └──────────┘   └──────────┘   └──────────┘         │      │
└──────────────────────────────────────────────────────┘      │
                                                               │
┌──────────────────────────────────────────────────────────────┘
│                    memory-store-mcp (:7708)
│  ┌──────────────────────────────────────────┐
│  │ memory/project/chat-machines/digest/{id}  │
│  └──────────────────────────────────────────┘
└──────────────────────────────────────────────┘
```

## Directory Layout

```
chat-machines/
├── main.go              # Entrypoint: flags, MCP server startup, engine init
├── server.go            # MCP server setup, tool registration, system instructions
├── agent.go             # OpenCodeServeClient: HTTP client for opencode-serve
├── room.go              # Room, Participant, Message, RoomStore, RoomSnapshot
├── engine.go            # ChatEngine: round-robin loop, goroutine per room
├── summary.go           # Post-chat summary collection → memory-store-mcp
├── store.go             # Optional SQLite persistence (enabled by --db flag)
├── agent_test.go        # Tests for agent client
├── room_test.go         # Tests for room store
├── engine_test.go       # Tests for chat engine
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

After chat stops (max_rounds, chat_stop, or auto-stop):
1. Each participant is asked to write a summary
2. All summaries are collected
3. A unified digest is saved to memory-store-mcp

**Rationale:** The value of chat is the knowledge generated, not the chat itself.

### 7. Human-in-Chat via Adjunct

Humans participate through their existing opencode session:
- Baron acts as a bridge (adjunct mode)
- Human messages flow: user → Baron → chat_create/chat_send → room
- Room messages flow: room → chat_get → Baron → user

**Rationale:** No special "human session" needed.

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
  --timeout 120s \                          # per-message timeout
  --log-dir /var/log/chat-machines          # file logging (optional)
```

## Future Extensions (not in v1)

- **Scenarios/Templates:** Predefined chat patterns
- **WebSocket streaming:** Real-time transcript delivery
- **Agent marketplace:** Register/catalog available agents
- **Dream Weaver integration:** Scheduled nightly chats
