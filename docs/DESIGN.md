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
├── main.go              # Entrypoint: flags, config load, MCP server startup, engine init
├── server.go            # MCP server setup, tool/resource registration, system instructions
├── agent.go             # OpenCodeServeClient: HTTP client for opencode-serve
├── config.go            # Agent registry config loader
├── config.json.example  # Checked-in example agent registry
├── room.go              # Room, Participant, Message, RoomStore, RoomSnapshot, adjunct state
├── engine.go            # ChatEngine: round-robin loop, goroutine per room, digest storage
├── summary.go           # Digest builder and optional memory-store-mcp saver
├── store.go             # Optional SQLite persistence (enabled by --db flag)
├── agent_test.go        # Tests for agent client
├── config_test.go       # Tests for config loader
├── room_test.go         # Tests for room store
├── engine_test.go       # Tests for chat engine and OK protocol
├── server_test.go       # Tests for MCP tools/resources
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

### 7a. Adjunct Round-Cycle Wait

When an adjunct is active, the engine pauses at the end of each round after collecting AI responses and waits for the human to speak. The synchronization is implemented via a buffered `humanReady chan struct{}` on the `Room`:

1. `SetAdjunct` creates the channel with capacity 1.
2. `AddExternalMessage` appends the human message to the transcript and signals the channel.
3. `runRound()` reads from the channel after recording AI responses.
4. The wait select also watches the room context, so `StopRoom`/`StopAll` abort the wait immediately and do not block until the timeout.
5. If the human does not respond within the configured timeout (default 5 minutes, configurable via `--adjunct-timeout`), the engine records an `(adjunct timeout)` marker and proceeds to the next round.
6. `ClearAdjunct` closes the channel, which unblocks the engine if it is currently waiting.
7. A new `SetAdjunct` call closes any previous channel first, so a rejoin cannot strand the engine on a stale channel.

**Rationale:** The buffered channel avoids polling latency/CPU waste, absorbs the race where the human sends a message before the engine reaches its wait select, and naturally handles multiple human messages per round (only the first signal is needed). Watching the context in the same select preserves the existing prompt shutdown semantics.

**WaitForRoundAdvance:** When a human or external participant sends a message, `WaitForRoundAdvance` polls until **all** expected non-sender participants have responded at the target round. The expected count is `len(Participants)` when the sender is an adjunct, or `len(Participants)-1` when the sender is a participant. It searches the post-increment round (`roundBefore+1`) for normal rounds and also the pre-increment round (`roundBefore`) when the room stops, so the final-reply round is captured. Timeout markers and OK responses count because their `From` field matches the participant name.

**Round counting:** `CurrentRound` is incremented at the **end** of `runRound()`, after the adjunct has spoken (or timed out). This ensures a round is considered complete only when **all participants** (AI + human) have contributed.

**Final reply round:** If `CurrentRound` reaches `MaxRounds` and the most recent transcript message is a real human adjunct message (not the synthetic `(adjunct timeout)` marker), the engine runs one additional AI response round (without incrementing the counter) before stopping. This prevents the human's final message from being left unanswered.

**Internal numbering:** `CurrentRound` starts at 0 and is 0-based internally (messages tagged 0, 1, 2 for a 3-round chat). AI prompts display `round+1` for human-friendly numbering (`[Round 1/3]`). API responses return the 0-based value directly.

### 8. Agent Interface for Testability

`ChatEngine` depends on the `Agent` interface, not directly on `*AgentClient`:

```go
type Agent interface {
	CreateSession(name, agent, model, workDir string, permissions []PermissionRule) (string, error)
	SendMessage(sessionID, text, agent, model string) (string, error)
	SendMessageAsync(sessionID, text, agent, model string) error
	CloseSession(sessionID string) error
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
  --adjunct-timeout 5m \                    # human adjunct response timeout (default: 5m)
  --log-dir /var/log/chat-machines          # file logging (optional)
```

## MCP Tool Reference

| Tool | Purpose |
|---|---|
| `chat_create` | Create and start a multi-AI chat room (1+ participants; human adjunct can be added via `chat_join`) |
| `chat_list` | List active rooms |
| `chat_get` | Get room state (includes `digest` for stopped rooms) |
| `chat_send` | Inject a message from a human into the room (also unblocks the round cycle when an adjunct is waiting) |
| `chat_join` | Join a room as a human adjunct |
| `chat_leave` | Leave a room as a human adjunct |
| `chat_stop` | Stop a room and return the summary digest |
| `chat_stats` | Global chat statistics |
| `chat_ask` | Ask a single synchronous question to another agent; no room is created |
| `chat_display_format` | Return the canonical relay format for human adjunct output |

## MCP Resource Reference

| Resource | Purpose |
|---|---|
| `chat-machines://agents` | List of agents available via `chat_ask` with models, specialties, and descriptions |

### `chat_ask` — Synchronous Agent Question

`chat_ask(agent, question, model?)` creates an ephemeral opencode-serve session for the named agent, sends a single question through the blocking `POST /session/{id}/message` endpoint, and returns the answer as `{agent, question, answer}`. The session is then explicitly closed via `DELETE /session/{id}` and is not tracked in `RoomStore`. It is intended for quick, one-off queries (e.g. asking Svetik to evaluate an idea) without the overhead of creating a full chat room.

Before the question is sent, it is prefixed with an instruction asking the agent to save a brief report to `memory-store-mcp` after answering. The instruction includes the exact memory key pattern (`memory/agent/chat-ask/log/YYYY-MM-DD/HHMMSS/<agent>`) and JSON format. This is a best-effort prompt instruction; the agent may or may not save the report depending on its capabilities.

**Design notes:**
- Reuses the existing `Agent` interface and `AgentClient` so no new HTTP client is needed.
- No room, transcript, digest, or summary is created.
- The caller provides the next question in a follow-up `chat_ask` call; multi-round dialogs are composed by the caller, not the engine.
- Session cleanup is explicit (`DELETE /session/{id}`) rather than relying on the polite async message workaround.

## 8. External Chat Connector (ECC)

External participants connect via HTTP instead of opencode-serve. They are full
round-robin participants in the room and are treated the same as AI participants
by the engine, except their "turn" is fulfilled by polling a dedicated endpoint.

### 8.1 Endpoint

```
GET /room/{room_id}/external?token={token}&body={URL-ENCODED_JSON}
```

The `body` parameter decodes to `{ "action": "connect" | "send", "message": "..." }`.
`token` is a UUID generated per external participant when the room is created.

### 8.2 Data Model

- `Participant.Type` — `"ai"` (default) or `"external"`
- `Participant.Role` — `"controller"` or `"participant"` (external only)
- `Participant.HumanType` — `"human"` or `"human+agent"` (external only)
- `Participant.token` — unexported UUID for endpoint auth
- `Participant.ready` — buffered `chan struct{}`, signalled when the external sends
- `Room.externalChannels` — maps participant name to the ready channel for HTTP handler lookup
- `Room.externalResps` — maps participant name to the latest pending response text

### 8.3 Round Cycle

`runRound` launches one goroutine per participant. AI participants use
`agent.SendMessage`; external participants wait on their `ready` channel with the
configured timeout. When the external endpoint receives a message it:

1. Validates the token and decodes the action body.
2. Stores the text in `externalResps`.
3. Signals the `ready` channel.

`runRound` is the sole writer of external participant transcript entries. It reads
the queued text via `GetExternalResponse`, appends the message with the current
round number, and applies the OK protocol just like an AI response. External
participants are skipped entirely in the final reply round; they receive no
synthetic transcript entries.

### 8.4 Backward Compatibility

- Empty `Participant.Type` is treated as `"ai"`.
- Existing rooms, tests, and callers work without changes.
- External endpoint is optional: disabled when `--external-addr` is empty.

## 9. Agent Registry Config (implemented — issue #18)

Agent metadata (name, model, specialty, when to use, description) is loaded from
`~/.config/chat-machines/config.json`. The config file defines which agents are
available via `chat_ask`.

**File format:**
```json
{
  "agents": [
    {
      "name": "svetik",
      "model": "deepseek/deepseek-v4-flash",
      "specialty": "архитектурный наблюдатель, мера идей",
      "when": "оценка идей, поиск слабых мест, ревью концепций",
      "description": "Svetik (Светик) — pure observation agent..."
    }
  ]
}
```

**Fallback:** If the config file is missing or invalid, a built-in default list
of 9 known agents is used.

**Layout:** `config.go` in module root provides `Config` struct, `LoadConfig()`,
`FindConfig()`, `DefaultConfig()`, `GetAgent()`, `AgentNames()`, `AgentSummaries()`.
Zero external dependencies.

## 10. MCP Resource `chat-machines://agents` (implemented — issue #18)

The MCP server exposes an `agents` resource at `chat-machines://agents`. Agents
with chat-machines MCP connected can read this resource to discover who is
available via `chat_ask`. The resource returns the agent list from config.json
as structured JSON via the `AgentSummaries()` helper.

## 11. chat_ask Loop Protection (implemented — issue #18)

When `chat_ask` creates a session, it passes a deny rule for `chat-machines*`
tool prefix to `POST /session`. This prevents the called agent from recursively
calling `chat_ask` or any other chat-machines tool.

**Changes:**
- `Agent` interface: `CreateSession` gets a `permissions []PermissionRule` parameter
- `PermissionRule` type: `{Prefix string, Deny bool}` — sent in `POST /session` body
- `AgentClient.CreateSession`: serializes permissions to JSON
- `AskAgent`: passes `{Prefix: "chat-machines*", Deny: true}` to deny all chat-machines tools
- `startRoom`: passes `nil` (normal rooms have no restrictions)
- All mock agents: signature updated (permissions ignored in mocks)

**Rationale:** Without loop protection, an agent called via `chat_ask` could itself
call `chat_ask`, creating infinite recursion. The opencode-serve enforces permission
rules at the tool dispatch level.

## Future Extensions (not in v1)

- **Scenarios/Templates:** Predefined chat patterns
- **WebSocket streaming:** Real-time transcript delivery
- **Agent marketplace:** Register/catalog available agents
- **Dream Weaver integration:** Scheduled nightly chats
