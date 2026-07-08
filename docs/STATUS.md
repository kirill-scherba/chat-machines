# STATUS.md — chat-machines

## Current Phase

**Issue #5 in review** — PR #6 created, addressing review feedback.

## Active Issues

| Number | Title | Status | Labels |
|---|---|---|---|
| #1 | feat: Чат Машин — Multi-AI chat rooms server | ✅ Done (merged) | `chat-machines`, `go` |
| #3 | feat: chat_send, human participation mode, digest fixes | 🟣 In review | `chat-machines`, `enhancement` |
| #5 | fix: adjunct mode — human participant must be waited on in round cycle | 🟣 In review | `chat-machines`, `bug`, `plan/approved`, `act/in-progress`, `act/review` |
| #6 | PR: fix: adjunct mode — wait for human in round cycle (#5) | 🟣 In review | — |

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
- `engine.go`: `AddExternalMessage`, `SetAdjunct`, `ClearAdjunct`, `SetSummaryAutoSave`, digest storage in `finishRoom`, per-room `completions` channel for synchronous `StopRoom`.
- `server.go`: `chatSendTool`, `chatJoinTool`, `chatLeaveTool`, updated `chatStopTool`, `roomSummary`, `roomDetail`, system instructions.
- `main.go`: `--auto-save` flag wired to engine.
- `engine_test.go`: `roundAwareMockAgent`, `TestOKProtocolMixedResponses`, `TestOKProtocolResets`, `TestEngineChatSendAndAdjunct`, `TestStopRoomReturnsDigest`.
- `docs/DESIGN.md`: updated diagram, design decisions, MCP tool reference.

## Issue #5 — Implementation In Review (PR #6)

### Goal

Fix adjunct mode so the engine waits for the human participant before finishing each round.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | Add `humanReady chan struct{}` to Room; signal from `AddExternalMessage` | `room.go` | ✅ |
| 2 | Channel lifecycle in `SetAdjunct`/`ClearAdjunct`; `HasAdjunct` snapshot field | `room.go` | ✅ |
| 3 | Adjunct wait block in `runRound()` after AI responses (select on `humanReady` + timeout + `ctx.Done()`) | `engine.go` | ✅ |
| 4 | `--adjunct-timeout` flag wired to engine | `main.go` | ✅ |
| 5 | Relax `chat_create` min participants 2→1; update system instructions | `server.go` | ✅ |
| 6 | 7 tests covering wait, timeout, multiple rounds, one participant, leave-unblocks, stop-while-waiting, rejoin | `engine_test.go` | ✅ |
| 7 | Update Memory Bank docs (DESIGN.md, STATUS.md) | `docs/*.md` | ✅ |

### Pull Request

- **PR #6**: https://github.com/kirill-scherba/chat-machines/pull/6

### Architecture Decisions

- **Channel-based wait**: buffered `humanReady chan struct{}` (capacity 1) avoids polling latency and naturally absorbs the race where the human sends before the engine enters its wait select.
- **Context-aware wait**: `runRound` receives the room context and aborts the adjunct wait on cancellation, so `StopRoom`/`StopAll` never hang on the timeout.
- **Safe rejoin**: `SetAdjunct` closes the old `humanReady` channel before creating a new one, preventing the engine from being stranded on a stale channel.
- **Human NOT in OK-protocol**: human timeout/presence is orthogonal to the AI OK-counter auto-stop.
- **Timeout default**: 5 minutes, configurable via `--adjunct-timeout`.
- **AI-only rooms unaffected**: adjunct check skipped when `AdjunctName == ""`.

### Verification

- `go build ./...` — ✅
- `go test -race ./...` — ✅
- `go test ./...` — ✅
- `go vet ./...` — ✅

## Last Updated

2026-07-08 — Issue #3 implemented by Master; PR created and moved to In review.
2026-07-08 — Issue #5 planned, approved, moved to In progress, and PR #6 created.
2026-07-08 — PR #6 review feedback addressed: context cancellation, safe rejoin, regression tests, docs sync.
