# STATUS.md — chat-machines

## Current Phase

**Implementation complete** — PR pending review.

## Issue

| Field | Value |
|---|---|
| Number | #1 |
| Title | feat: Чат Машин (chat-machines) — Multi-AI chat rooms server with MCP management |
| Labels | `plan/draft`, `chat-machines`, `architecture`, `go`, `plan/manager_assigned`, `plan/manager_checked`, `plan/improve`, `plan/approved`, `act/in-progress` |
| Branch | `feature/1-chat-machines` |
| Board | Matrica — In progress |

## Implementation Status

| Phase | Description | Status |
|---|---|---|
| Phase 1 | Project Infrastructure (go.mod, main.go, README, logging) | ✅ Complete |
| Phase 2 | Agent Client (opencode-serve HTTP) | ✅ Complete |
| Phase 3 | Room Management (types, store, CRUD) | ✅ Complete |
| Phase 4 | Chat Engine (round-robin, OK-protocol, timeouts) | ✅ Complete |
| Phase 5 | MCP Server (5 tools) | ✅ Complete |
| Phase 6 | Persistence (SQLite optional, memory-store summary) | ✅ Complete |
| Phase 7 | Tests | ✅ Complete |

## Verification

- `go build ./...` — ✅
- `go test ./...` — ✅
- `go vet ./...` — ✅

## Key Changes from Plan

1. **Introduced `Agent` interface** in `engine.go` so the chat engine can be tested with a mock implementation. `*AgentClient` implements `Agent`.
2. **Added `SetRoundDelay`** to make engine tests deterministic and prevent fast mock clients from exhausting max_rounds before `StopRoom` is called.
3. **SQLite persistence is optional** (enabled by `--db` flag) and implemented directly with `database/sql` + `mattn/go-sqlite3`, not via `sqlh`.

## Files Added/Modified

| File | Role |
|---|---|
| `main.go` | Entrypoint, flag parsing, MCP server startup, graceful shutdown |
| `agent.go` | OpenCodeServe HTTP client |
| `room.go` | Room, Participant, Message, RoomStore |
| `engine.go` | ChatEngine: round-robin loop, OK-protocol, summary collection |
| `server.go` | MCP server + 5 tools |
| `summary.go` | memory-store-mcp digest persistence |
| `store.go` | Optional SQLite message logging |
| `*_test.go` | Unit and integration tests |
| `README.md` | Updated with run instructions |
| `docs/CONTEXT.md`, `docs/DESIGN.md`, `docs/STATUS.md` | Memory Bank docs |
| `.gitignore` | Added binary and SQLite patterns |

## Next Steps

1. Create Pull Request
2. Move Matrica issue to In review
3. Add `act/review` label
4. Wait for His Majesty's review

## Last Updated

2026-07-07 — Implementation complete by Master
