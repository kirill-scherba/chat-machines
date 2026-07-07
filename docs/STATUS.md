# STATUS.md — chat-machines

## Current Phase

**Review fixes applied** — awaiting final re-review of PR #2.

## Issue

| Field | Value |
|---|---|
| Number | #1 |
| Title | feat: Чат Машин (chat-machines) — Multi-AI chat rooms server with MCP management |
| Labels | `plan/draft`, `chat-machines`, `architecture`, `go`, `plan/manager_assigned`, `plan/manager_checked`, `plan/improve`, `plan/approved`, `act/review` |
| Branch | `feature/1-chat-machines` |
| Board | Matrica — In review |

## Implementation Status

| Phase | Description | Status |
|---|---|---|
| Phase 1 | Project Infrastructure (go.mod, main.go, README, logging) | ✅ Complete |
| Phase 2 | Agent Client (opencode-serve HTTP) | ✅ Complete |
| Phase 3 | Room Management (types, store, CRUD, snapshots) | ✅ Complete |
| Phase 4 | Chat Engine (round-robin, OK-protocol, timeouts) | ✅ Complete |
| Phase 5 | MCP Server (5 tools) | ✅ Complete |
| Phase 6 | Persistence (SQLite optional, memory-store summary) | ✅ Complete |
| Phase 7 | Tests | ✅ Complete |

## Verification

- `go build ./...` — ✅
- `go test -race ./...` — ✅
- `go test ./...` — ✅
- `go vet ./...` — ✅

## Key Changes from Plan

1. **Introduced `Agent` interface** in `engine.go` so the chat engine can be tested with a mock implementation. `*AgentClient` implements `Agent`.
2. **Added `SetRoundDelay`** to make engine tests deterministic and prevent fast mock clients from exhausting max_rounds before `StopRoom` is called.
3. **SQLite persistence is optional** (enabled by `--db` flag) and implemented directly with `database/sql` + `mattn/go-sqlite3`, not via `sqlh`.
4. **Removed per-room `save_log` parameter** from `chat_create`: SQLite logging is now global (`--db`) and not per-room, matching the actual implementation.
5. **Introduced `RoomSnapshot`** in `room.go`: immutable, lock-protected copies of room state for MCP handlers and tests, eliminating data races.
6. **Made `StopRoom` synchronous on status**: after `StopRoom` returns, the room status is deterministically `RoomStopped`, even if the goroutine is still winding down.

## Files Added/Modified

| File | Role |
|---|---|
| `main.go` | Entrypoint, flag parsing, MCP server startup, graceful shutdown |
| `agent.go` | OpenCodeServe HTTP client |
| `room.go` | Room, Participant, Message, RoomStore, RoomSnapshot |
| `engine.go` | ChatEngine: snapshot-based external reads, deterministic StopRoom |
| `server.go` | MCP server + 5 tools, snapshot-based responses |
| `summary.go` | memory-store-mcp digest persistence |
| `store.go` | Optional SQLite message logging |
| `*_test.go` | Unit and integration tests |
| `README.md` | Updated with run instructions |
| `docs/CONTEXT.md`, `docs/DESIGN.md`, `docs/STATUS.md` | Memory Bank docs |
| `.gitignore` | Added binary and SQLite patterns |

## Next Steps

1. Address any remaining review comments on PR #2.
2. Wait for His Majesty's final approval.
3. Merge PR #2 and close issue #1.
4. Move Matrica issue to Done.

## Last Updated

2026-07-07 — Final review fixes (gofmt, docs, instructions) applied by Master
