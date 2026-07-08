# STATUS.md — chat-machines

## Current Phase

**Issue #3 implemented** — branch `feature/3` ready, pull request in review.

## Active Issues

| Number | Title | Status | Labels |
|---|---|---|---|
| #1 | feat: Чат Машин — Multi-AI chat rooms server | ✅ Done (merged) | `chat-machines`, `go` |
| #3 | feat: chat_send, human participation mode, digest fixes | 🟣 In review | `chat-machines`, `enhancement` |

## Issue #3 — Implementation Complete

Implemented in branch `feature/3`. Pull request created.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | `chat_send` MCP tool | `room.go`, `engine.go`, `server.go` | ✅ |
| 2 | Adjunct mode (`chat_join`/`chat_leave`) | `room.go`, `engine.go`, `server.go` | ✅ |
| 3 | Digest in response (opt-in `--auto-save`) | `room.go`, `engine.go`, `main.go`, `server.go` | ✅ |
| 4 | 3+ participant OK-protocol tests | `engine_test.go` | ✅ |

### Architecture Decisions (final)

- **`chat_send`**: `RoomStore.AddExternalMessage` appends a `Message` with the current round, no turn increment.
- **Adjunct mode**: `Room` tracks `AdjunctSessionID`/`AdjunctName` as metadata; `chat_join`/`chat_leave` mutate it.
- **Digest**: `Room.StoredDigest` holds the post-chat summary; `chat_stop` returns `digest`; `chat_get` includes it for stopped rooms.
- **Auto-save**: `ChatEngine.summaryAutoSave` controlled by `--auto-save` flag (default `false`); preserves backward compatibility.

### Files Modified

- `room.go`: `AddExternalMessage`, `SetAdjunct`, `ClearAdjunct`, `SetDigest`, snapshot fields.
- `engine.go`: `AddExternalMessage`, `SetAdjunct`, `ClearAdjunct`, `SetSummaryAutoSave`, digest storage in `finishRoom`.
- `server.go`: `chatSendTool`, `chatJoinTool`, `chatLeaveTool`, updated `chatStopTool`, `roomSummary`, `roomDetail`, system instructions.
- `main.go`: `--auto-save` flag wired to engine.
- `engine_test.go`: `roundAwareMockAgent`, `TestOKProtocolMixedResponses`, `TestOKProtocolResets`, `TestEngineChatSendAndAdjunct`.
- `docs/DESIGN.md`: updated diagram, design decisions, MCP tool reference.

## Verification

- `go build ./...` — ✅
- `go test -race ./...` — ✅
- `go test ./...` — ✅
- `go vet ./...` — ✅

## Last Updated

2026-07-08 — Issue #3 implemented by Master; PR created and moved to In review.
