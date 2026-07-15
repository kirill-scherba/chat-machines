# STATUS.md — chat-machines

## Current Phase

**Issue #18 in review** — PR #19 implementing agent registry config, MCP resource `chat-machines://agents`, `chat_ask` loop protection, and README/Memory Bank sync is awaiting re-review.

## Active Issues

| Number | Title | Status | Labels |
|---|---|---|---|
| #1 | feat: Чат Машин — Multi-AI chat rooms server | ✅ Done (merged) | `chat-machines`, `go` |
| #3 | feat: chat_send, human participation mode, digest fixes | ✅ Done (merged) | `chat-machines`, `enhancement` |
| #5 | fix: adjunct mode — human participant must be waited on in round cycle | ✅ Done (merged) | `chat-machines`, `bug`, `plan/approved` |
| #6 | PR: fix: adjunct mode — wait for human in round cycle (#5) | ✅ Done (merged) | — |
| #7 | fix: human as full participant — round cycle must count human reply | ✅ Done (merged) | `bug`, `chat-machines`, `plan/approved`, `act/in-progress`, `act/review` |
| #8 | feat: External Chat Connector (ECC) — HTTP bridge | ✅ Done (merged) | `enhancement`, `plan/draft`, `plan/improve`, `plan/approved`, `act/in-progress`, `act/review` |
| #9 | PR: fix: human as full participant — count round after everyone speaks (#7) | ✅ Done (merged) | — |
| #12 | fix: WaitForRoundAdvance returns before all AI participants have responded | ✅ Done (merged) | `bug`, `chat-machines`, `plan/improve`, `plan/manager_checked`, `plan/approved`, `act/in-progress` |
| #14 | feat: chat_ask — synchronous agent question | ✅ Done (merged) | `enhancement`, `chat-machines`, `plan/approved`, `act/in-progress`, `act/review` |
| #16 | fix: chat_ask — session closing, logging calls to memory | ✅ Done (merged) | `enhancement`, `chat-machines`, `plan/improve`, `plan/approved` |
| #18 | feat: agent registry config, MCP resource, and chat_ask loop protection | 🟣 In review (PR #19) | `enhancement`, `chat-machines`, `plan/approved`, `act/in-progress`, `act/review` |

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

## Issue #7 — Implementation In Review (PR #9)

### Goal

Fix the round cycle so the human adjunct is a full participant: a round is complete only when all AIs and the human have spoken. This resolves both the "last-message loss" bug and the "round counting" bug.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | Move round increment from start to end of `runRound()` | `engine.go` | ✅ |
| 2 | Update first-round detection and transcript display for 0-based numbering | `engine.go` | ✅ |
| 3 | Update test assertions for 0-based message round numbers | `engine_test.go` | ✅ |
| 4 | Rewrite `TestAdjunctMultipleRounds` goroutine to detect AI round messages | `engine_test.go` | ✅ |
| 5 | Add `TestHumanLastMessageNotLost` covering the exact bug scenario | `engine_test.go` | ✅ |
| 6 | Update Memory Bank docs (DESIGN.md, STATUS.md) | `docs/*.md` | ✅ |

### Architecture Decisions

- **Post-round increment**: `CurrentRound` is incremented after the adjunct has spoken (or timed out).
- **Final reply round**: When `max_rounds` is reached and the last transcript message is a real human adjunct message (not the synthetic timeout marker), the engine runs one additional AI response round (without incrementing the counter) so the final human message is answered before the room stops.
- **0-based internal numbering**: Messages are tagged with round numbers `0, 1, 2` for a 3-round chat. AI prompts still display `[Round 1/3]`, `[Round 2/3]`, etc. No API rename of `adjunct`.
- **Max-rounds check after increment**: `CurrentRound >= MaxRounds` is checked after a round completes, not before it starts.

### Pull Request

- **PR #9**: https://github.com/kirill-scherba/chat-machines/pull/9

### Verification

- `go build ./...` — ✅
- `go vet ./...` — ✅
- `go test -race ./...` — ✅

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

## Issue #16 — Implementation In Progress

### Goal

Improve `chat_ask` (#14): explicitly close opencode-serve sessions via `DELETE /session/{id}` and prepend a memory-logging instruction to the question.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | `CloseSession` method in `AgentClient` (HTTP DELETE) | `agent.go` | ✅ |
| 2 | Extend `Agent` interface with `CloseSession` | `engine.go` | ✅ |
| 3 | Update `AskAgent` to call `CloseSession` and prepend memory-logging prefix | `engine.go` | ✅ |
| 4 | Update `closeSessions` to use `CloseSession` instead of async polite message | `engine.go` | ✅ |
| 5 | Update mocks (`mockAgentClient`, `roundAwareMockAgent`, `delayingMockAgent`) | `engine_test.go` | ✅ |
| 6 | Tests: `TestAskAgent`, `TestAskAgentCloseSessionOnError`, `TestBuildAskAgentLogPrefix`, `TestAgentClientCloseSession` | `engine_test.go`, `agent_test.go` | ✅ |
| 7 | Update Memory Bank docs (CONTEXT.md, DESIGN.md, STATUS.md) | `docs/*.md` | ✅ |

### Architecture Decisions

- `CloseSession` is part of the `Agent` interface so integration tests can mock it deterministically.
- `AgentClient.CloseSession` sends `DELETE /session/{id}` with a 10s timeout and tolerates 200/204/404.
- `AskAgent` prepends the memory-logging instruction to the question; the agent may save a report to `memory/agent/chat-ask/log/YYYY-MM-DD/HHMMSS/<agent>` if it supports the `memory_save` tool.
- Room session cleanup (`closeSessions`) uses the same explicit `CloseSession` instead of the previous polite async message workaround.

### Verification

- `go build ./...` — ✅
- `go vet ./...` — ✅
- `go test ./...` — ✅

## Issue #14 — Implementation Done (Merged)

### Goal

Add the `chat_ask` MCP tool for synchronous one-off questions to an opencode-serve agent without creating a chat room.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | `AskAgent` engine method (ephemeral session, blocking send, close) | `engine.go` | ✅ |
| 2 | `chat_ask` MCP tool registration and handler | `server.go` | ✅ |
| 3 | Validation and error responses for missing agent/question | `server.go`, `engine.go` | ✅ |
| 4 | Tests for `AskAgent` with mock agent (success + validation) | `engine_test.go` | ✅ |
| 5 | MCP-level tests for `chat_ask` result shape and validation | `server_test.go` | ✅ |
| 6 | Update system instructions to list `chat_ask` | `server.go` | ✅ |
| 7 | Update Memory Bank docs (DESIGN.md, STATUS.md) | `docs/*.md` | ✅ |

### Architecture Decisions

- `chat_ask` reuses the existing `Agent` interface and `AgentClient`; no new HTTP client or transport is introduced.
- The session name is `ask-<agent>` to make ephemeral sessions distinguishable in opencode-serve logs.
- `AskAgent` performs one blocking `SendMessage`, then closes the session. No room state is mutated.
- Multi-round dialogs are composed by the caller via multiple `chat_ask` calls; v1 does not implement an internal dialog loop.

### Verification

- `go build ./...` — ✅
- `go vet ./...` — ✅
- `go test ./...` — ✅
- `go test -race ./...` — ✅

## Issue #8 — Implementation In Review (PR #10)

### Goal

Add External Chat Connector (ECC) — HTTP bridge that lets external AI services
(ChatGPT, DeepSeek, Claude, Kimi) participate as first-class chat room members.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | Extend `Participant` with Type/Role/HumanType, token, ready channel | `room.go` | ✅ |
| 2 | Extend `Room` with externalChannels/externalResps maps | `room.go` | ✅ |
| 3 | `RoomStore.AddExternalParticipantMessage` + token lookup helpers | `room.go` | ✅ |
| 4 | Extend `ParticipantConfig` and `CreateChatRoom` for externals | `engine.go` | ✅ |
| 5 | `startRoom` skips opencode sessions for externals | `engine.go` | ✅ |
| 6 | `runRound` waits on per-participant channels for externals | `engine.go` | ✅ |
| 7 | `finishRoom` skips external participants in summaries | `engine.go` | ✅ |
| 8 | New `GET /room/{id}/external` endpoint (connect/send) | `external.go` | ✅ |
| 9 | Skill template for `human+agent` participants | `external.go` | ✅ |
| 10 | `chat_create` accepts type/role/human_type, returns `external_urls` | `server.go` | ✅ |
| 11 | `--external-addr` and `--external-base-url` flags | `main.go` | ✅ |
| 12 | Engine tests for external participants | `engine_test.go` | ✅ |
| 13 | External endpoint tests | `external_test.go` | ✅ |
| 14 | Review fixes: transcript ownership, final reply skip, exact-count tests | `engine.go`, `room.go`, `engine_test.go`, `external_test.go` | ✅ |
| 15 | Update DESIGN.md and STATUS.md | `docs/*.md` | ✅ |

### Architecture Decisions

- **External participants are round-robin members**: they participate alongside AI participants, not as adjunct observers.
- **Per-participant signalling**: buffered `ready chan struct{}` per external participant (capacity 1) absorbs race where the external sends before the engine reaches its wait select.
- **Token-based auth**: UUID token stored as unexported `Participant.token`; not serialized in snapshots or logs.
- **Single GET endpoint**: `GET /room/{room_id}/external?token=...&body=...` for ChatGPT Actions compatibility.
- **Skill template**: returned on `connect` for `human+agent` type to guide the external AI assistant.
- **Transcript ownership**: `runRound` is the sole writer of external participant transcript entries; `AddExternalParticipantMessage` only queues the text and signals the ready channel.
- **Backward compatibility**: empty `Participant.Type` treated as `"ai"`; existing callers and tests unchanged.

### Pull Request

- **PR #10**: https://github.com/kirill-scherba/chat-machines/pull/10

### Verification

- `go build ./...` — ✅
- `go vet ./...` — ✅
- `go test ./...` — ✅
- `go test -race ./...` — ✅

## Issue #12 — Implementation In Progress

### Goal

Fix `WaitForRoundAdvance` so it returns only after ALL AI participants have responded
in the target round, not after the first one. Also fix the `runFinalReply` edge case
where final-reply messages are stored at a different round than `WaitForRoundAdvance`
expects.

### Completed Items

| # | Item | Files | Status |
|---|---|---|---|
| 1 | Add `delayingMockAgent` for staggered-response tests | `engine_test.go` | ✅ |
| 2 | Add `hasAllExpectedResponses` helper | `engine.go` | ✅ |
| 3 | Rewrite `WaitForRoundAdvance` to wait for all expected responses | `engine.go` | ✅ |
| 4 | Handle `runFinalReply` edge case (check both `roundBefore` and `roundBefore+1` on room stop) | `engine.go` | ✅ |
| 5 | Add `TestWaitForRoundAdvanceAllParticipants` with slow responder | `engine_test.go` | ✅ |
| 6 | Update DESIGN.md and STATUS.md | `docs/*.md` | ✅ |

### Architecture Decisions

- **Unique-name counting**: `map[string]bool` over messages at the target round(s), keyed by `m.From`. Timeout markers count because their `From` field matches the participant name.
- **Double-round search on stop**: When the room stops mid-wait, check both `roundBefore` (`runFinalReply`) and `roundBefore+1` (normal round).
- **Sender exclusion**: If `senderName` is in `Participants`, expect `len(Participants)-1` responses. If not (adjunct), expect `len(Participants)`.

### Verification

- `go test -race ./...` — ✅

## Last Updated

2026-07-15 — Issue #18 implemented by Master; PR #19 created and moved to In review. README and Memory Bank docs synced with MCP surface.
2026-07-15 — Issue #14 implemented by Master; PR created and moved to In review.
2026-07-08 — Issue #3 implemented by Master; PR created and moved to In review.
2026-07-08 — Issue #5 planned, approved, moved to In progress, and PR #6 created.
2026-07-08 — PR #6 review feedback addressed: context cancellation, safe rejoin, regression tests, docs sync.
