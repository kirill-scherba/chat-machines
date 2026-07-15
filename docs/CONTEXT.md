# CONTEXT.md — chat-machines

## Project

**chat-machines** — Multi-AI chat rooms server with MCP management.
First step toward Dream Weaver (Сны Машины).

## Repository

- **GitHub:** `kirill-scherba/chat-machines`
- **Language:** Go 1.26+
- **Module:** `github.com/kirill-scherba/chat-machines`

## Key Dependencies

| Dependency | Version | Purpose |
|---|---|---|
| `mark3labs/mcp-go` | v0.54.1 | MCP server framework |
| `github.com/google/uuid` | v1.6.0 | Room ID generation |
| `github.com/kirill-scherba/log` | v0.1.0 | Structured logging (zero deps) |
| `github.com/mattn/go-sqlite3` | v1.14.24 | SQLite driver (optional persistence) |

## External Systems

| System | Endpoint | Purpose |
|---|---|---|
| opencode-serve | `http://127.0.0.1:7709` | Agent session management (create, send, receive) |
| memory-store-mcp | `http://127.0.0.1:7708/mcp` | Chat summary persistence |

## opencode-serve HTTP API

Reference: `alice-baron/opencode_serve.go`

```
POST /session                           — Create session
  Body: {"name": "...", "agent": "Baron", "directory": "/path"}
  Response: {"id": "ses_..."}

POST /session/{id}/message              — Send message (blocking, waits for response)
  Body: {"parts": [{"type":"text","text":"..."}], "agent": "Baron", "noReply": false,
         "model": {"providerID":"ollama-cloud","modelID":"deepseek-v4-flash"}}
  Response: {"info": {"id":"...","sessionID":"..."}, "parts": [{"type":"text","text":"..."}]}

POST /session/{id}/prompt_async         — Send message (non-blocking, returns 204)

GET /session/{id}/message?limit=N      — Read messages from session

DELETE /session/{id}                    — Delete/close session (returns 204 or 404)
```

## Related Projects

| Project | Relevance |
|---|---|
| `alice-baron` | Reference implementation of opencode-serve HTTP client |
| `memory-store-mcp` | Patterns: MCP server, flat layout, manual DI |
| `orchestrator-watchdog` | opencode CLI integration, session lifecycle management |

## Architecture Decisions

See `DESIGN.md` for full architecture documentation.

## Naming Conventions

- Package name: `main` (flat layout, all files at module root)
- File names: lowercase, descriptive (room.go, agent.go, engine.go)
- Exported names: `NewChatEngine`, `Agent`, `RoomStore`
- Test files: `*_test.go` alongside source
