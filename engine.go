// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Chat engine: round-robin loop orchestrator.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// PermissionRule is a tool access rule for an agent session.
// Prefix is a glob-style pattern matching MCP tool names (e.g. "chat-machines*").
// Deny=true blocks matching tools; Deny=false allows them.
type PermissionRule struct {
	Prefix string `json:"prefix"`
	Deny   bool   `json:"deny"`
}

// Agent is the interface for creating sessions and sending/receiving messages.
// It abstracts the opencode-serve HTTP client so tests can use mocks.
type Agent interface {
	CreateSession(name, agent, model, workDir string, permissions []PermissionRule) (string, error)
	SendMessage(sessionID, text, agent, model string) (string, error)
	SendMessageAsync(sessionID, text, agent, model string) error
	CloseSession(sessionID string) error
}

// ChatEngine manages the lifecycle of all chat rooms.
type ChatEngine struct {
	store           *RoomStore
	agent           Agent
	logger          ChatLogger
	summary         SummarySaver
	summaryAutoSave bool
	roundDelay      time.Duration
	adjunctTimeout  time.Duration

	// memoryMCPURL is the HTTP URL of memory-store-mcp for saving chat_ask logs.
	memoryMCPURL string

	// active rooms keyed by room id; value is the cancel function for the room goroutine.
	active map[string]context.CancelFunc
	// completions keyed by room id; closed when the room goroutine finishes cleanup.
	completions map[string]chan struct{}
	mu          sync.RWMutex
}

// NewChatEngine creates a new chat engine.
func NewChatEngine(agent Agent) *ChatEngine {
	return &ChatEngine{
		store:       NewRoomStore(),
		agent:       agent,
		active:      make(map[string]context.CancelFunc),
		completions: make(map[string]chan struct{}),
		roundDelay:  500 * time.Millisecond,
	}
}

// SetRoundDelay configures the pause between rounds. Useful in tests.
func (e *ChatEngine) SetRoundDelay(d time.Duration) {
	e.roundDelay = d
}

// SetAdjunctTimeout configures how long the engine waits for a human adjunct
// to respond before continuing a round. Default is 5 minutes.
func (e *ChatEngine) SetAdjunctTimeout(d time.Duration) {
	e.adjunctTimeout = d
}

// SetLogger sets an optional message logger.
func (e *ChatEngine) SetLogger(l ChatLogger) {
	e.logger = l
}

// SetSummarySaver sets the summary saver used when a room stops.
func (e *ChatEngine) SetSummarySaver(s SummarySaver) {
	e.summary = s
}

// SetMemoryMCPURL sets the memory-store-mcp HTTP URL for saving chat_ask logs.
func (e *ChatEngine) SetMemoryMCPURL(url string) {
	e.memoryMCPURL = url
}

// saveAskAgentLog saves a chat_ask Q&A pair to memory-store-mcp via direct POST.
func (e *ChatEngine) saveAskAgentLog(agentName, question, answer string) {
	ts := time.Now().UTC()
	dateStr := ts.Format("2006-01-02")
	timeStr := ts.Format("150405")
	key := fmt.Sprintf("memory/agent/chat-ask/log/%s/%s/%s", dateStr, timeStr, agentName)

	val := map[string]any{
		"timestamp":        ts.Format(time.RFC3339),
		"agent":            agentName,
		"caller":           "baron (chat_ask)",
		"question_brief":   truncate(question, 100),
		"response_summary": truncate(answer, 100),
	}
	valueJSON, _ := json.Marshal(val)
	text := fmt.Sprintf("chat_ask: agent=%q question=%q answer=%q", agentName, question, answer)

	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_save",
			"arguments": map[string]any{
				"key":   key,
				"value": string(valueJSON),
				"text":  text,
			},
		},
		"id": 1,
	}
	raw, _ := json.Marshal(body)

	url := e.memoryMCPURL
	if url == "" {
		url = "http://127.0.0.1:7708/mcp"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		log.Printf("saveAskAgentLog: new request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("saveAskAgentLog: http: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("saveAskAgentLog: status %d: %s", resp.StatusCode, string(b))
		return
	}
	log.Printf("saveAskAgentLog: saved %s", key)
}

// truncate returns the first n runes of s.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// SetSummaryAutoSave controls whether the engine saves the digest to
// memory-store-mcp automatically on room stop. Default is false: the digest
// is returned in the chat_stop response for the caller to save.
func (e *ChatEngine) SetSummaryAutoSave(auto bool) {
	e.summaryAutoSave = auto
}

// CreateChatRoom creates and starts a chat room asynchronously.
// mode: "rounds" (default) or "roundless".
func (e *ChatEngine) CreateChatRoom(topic string, configs []ParticipantConfig, maxRounds int, mode string) (string, error) {
	room := e.store.CreateRoom(topic, maxRounds, mode)
	room.externalChannels = make(map[string]chan struct{})
	room.externalResps = make(map[string]string)

	for _, cfg := range configs {
		p := Participant{
			Name:      cfg.Name,
			Agent:     strings.ToLower(cfg.Agent),
			Model:     cfg.Model,
			Type:      cfg.Type,
			Role:      cfg.Role,
			HumanType: cfg.HumanType,
		}
		// Backward compatibility: treat empty type as AI.
		if p.Type == "" {
			p.Type = "ai"
		}
		if p.Name == "" {
			p.Name = fmt.Sprintf("agent-%d", len(room.Participants)+1)
		}
		if p.IsExternal() {
			p.token = uuid.New().String()
			p.ready = make(chan struct{}, 1)
			room.externalChannels[p.Name] = p.ready
		}
		if err := e.store.AddParticipant(room.ID, p); err != nil {
			return "", err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	e.mu.Lock()
	e.active[room.ID] = cancel
	e.completions[room.ID] = done
	e.mu.Unlock()

	go e.run(ctx, room, done)
	return room.ID, nil
}

// GetRoom returns the live room by id.
// Prefer GetRoomSnapshot for callers that need safe concurrent reads.
func (e *ChatEngine) GetRoom(id string) *Room {
	return e.store.GetRoom(id)
}

// GetRoomSnapshot returns a safe snapshot of a room.
func (e *ChatEngine) GetRoomSnapshot(id string) (RoomSnapshot, bool) {
	return e.store.GetSnapshot(id)
}

// WaitForRoundAdvance polls the room until the round has advanced AND messages
// from ALL non-sender participants appear in the new round. It also accounts for
// the final-reply round (runFinalReply), where AI responses are stored at the
// pre-increment round number. Used by MCP chat_send and ECC so the sender
// receives the complete set of AI responses, not just a confirmation that their
// message was accepted.
func (e *ChatEngine) WaitForRoundAdvance(roomID string, roundBefore int, senderName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap, ok := e.GetRoomSnapshot(roomID)
		if !ok {
			return
		}
		if snap.Status != RoomActive {
			// Room stopped. Check for final-reply messages at the pre-increment
			// round in case runFinalReply finished the room.
			if e.hasAllExpectedResponses(snap, roundBefore, senderName) ||
				e.hasAllExpectedResponses(snap, roundBefore+1, senderName) {
				return
			}
			return
		}
		if snap.CurrentRound > roundBefore {
			if e.hasAllExpectedResponses(snap, roundBefore+1, senderName) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// hasAllExpectedResponses reports whether every expected non-sender participant
// has at least one message at the given round. If senderName matches a member
// of snap.Participants, that sender is excluded; otherwise (adjunct), all
// participants are expected to respond. Timeout markers and OK responses count
// because their From field matches the participant name.
func (e *ChatEngine) hasAllExpectedResponses(snap RoomSnapshot, round int, senderName string) bool {
	expected := len(snap.Participants)
	for _, p := range snap.Participants {
		if p.Name == senderName {
			expected--
			break
		}
	}

	seen := make(map[string]bool)
	for _, m := range snap.Messages {
		if m.Round == round && m.From != senderName {
			seen[m.From] = true
		}
	}
	return len(seen) >= expected
}

// AddExternalMessage injects a message from an external source (e.g. human) into
// the room transcript without assigning a turn in the round-robin loop.
func (e *ChatEngine) AddExternalMessage(roomID, from, text string) error {
	return e.store.AddExternalMessage(roomID, from, text)
}

// SetAdjunct registers a human adjunct for the room.
func (e *ChatEngine) SetAdjunct(roomID, sessionID, name string) error {
	return e.store.SetAdjunct(roomID, sessionID, name)
}

// ClearAdjunct removes a human adjunct from the room.
func (e *ChatEngine) ClearAdjunct(roomID string) error {
	return e.store.ClearAdjunct(roomID)
}

// ListRooms returns all rooms.
func (e *ChatEngine) ListRooms() []*Room {
	return e.store.ListRooms()
}

// ListRoomSnapshots returns safe snapshots of all rooms.
func (e *ChatEngine) ListRoomSnapshots() []RoomSnapshot {
	return e.store.ListSnapshots()
}

// StopRoom stops a room gracefully and waits for the room goroutine to finish
// cleanup, including digest collection.
func (e *ChatEngine) StopRoom(id string) error {
	e.mu.Lock()
	cancel, ok := e.active[id]
	var done chan struct{}
	if ok {
		delete(e.active, id)
		done = e.completions[id]
		delete(e.completions, id)
	}
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("room %s is not active", id)
	}
	// Mark the room as stopping synchronously so MCP handlers observe a deterministic state.
	_ = e.store.SetStatus(id, RoomStopped)
	cancel()
	if done != nil {
		<-done
	}
	return nil
}

// StopAll stops all active rooms.
func (e *ChatEngine) StopAll() {
	e.mu.Lock()
	items := make([]context.CancelFunc, 0, len(e.active))
	dones := make([]chan struct{}, 0, len(e.completions))
	for _, cancel := range e.active {
		items = append(items, cancel)
	}
	for _, done := range e.completions {
		dones = append(dones, done)
	}
	e.active = make(map[string]context.CancelFunc)
	e.completions = make(map[string]chan struct{})
	e.mu.Unlock()
	for _, cancel := range items {
		cancel()
	}
	for _, done := range dones {
		<-done
	}
}

// run is the per-room goroutine. done is closed after finishRoom returns so
// callers can wait for completion of cleanup (including digest collection).
func (e *ChatEngine) run(ctx context.Context, room *Room, done chan struct{}) {
	defer close(done)
	log.Printf("room %s: starting with %d participant(s)", room.ID, room.ParticipantCount())

	if err := e.startRoom(room); err != nil {
		log.Printf("room %s: start failed: %v", room.ID, err)
		e.finishRoom(room, err)
		return
	}

	if err := e.store.SetStatus(room.ID, RoomActive); err != nil {
		log.Printf("room %s: set active failed: %v", room.ID, err)
	}

	if room.Mode == "roundless" {
		// Roundless: no round loop, just wait for stop signal.
		// External participants trigger AI responses directly via the handler.
		<-ctx.Done()
		e.finishRoom(room, ctx.Err())
		return
	}

	for {
		select {
		case <-ctx.Done():
			e.finishRoom(room, ctx.Err())
			return
		default:
		}

		done, err := e.runRound(ctx, room)
		if err != nil {
			log.Printf("room %s: round error: %v", room.ID, err)
			e.finishRoom(room, err)
			return
		}
		if done {
			log.Printf("room %s: stopping (%d rounds completed)", room.ID, room.GetCurrentRound())
			e.finishRoom(room, nil)
			return
		}
		if room.GetCurrentRound() >= room.GetMaxRounds() {
			log.Printf("room %s: reached max rounds", room.ID)
			e.finishRoom(room, nil)
			return
		}
	}
}

// startRoom creates opencode sessions and sends onboarding messages.
func (e *ChatEngine) startRoom(room *Room) error {
	// Take a snapshot so we can do network calls without holding room.mu.
	snap := room.Snapshot()

	workDir := "/tmp"
	sessionIDs := make([]string, len(snap.Participants))
	for i, p := range snap.Participants {
		if p.IsExternal() {
			sessionIDs[i] = ""
			continue
		}
		sid, err := e.agent.CreateSession(p.Name, p.Agent, p.Model, workDir, nil)
		if err != nil {
			return fmt.Errorf("create session for %s: %w", p.Name, err)
		}
		sessionIDs[i] = sid
	}

	onboarding := buildOnboarding(room.Topic, snap.Participants)
	for i, p := range snap.Participants {
		if p.IsExternal() {
			continue
		}
		// Send onboarding asynchronously — we don't need the response,
		// it's just context for the session. Saves 5-9s per room start.
		if err := e.agent.SendMessageAsync(sessionIDs[i], onboarding, p.Agent, p.Model); err != nil {
			return fmt.Errorf("onboarding %s: %w", p.Name, err)
		}
	}

	// Apply session IDs under lock.
	room.mu.Lock()
	defer room.mu.Unlock()
	if len(room.Participants) != len(sessionIDs) {
		return fmt.Errorf("participants changed during session creation")
	}
	for i := range room.Participants {
		room.Participants[i].SessionID = sessionIDs[i]
	}
	return nil
}

// runRound executes a single round. Returns true if the chat should stop due
// to all-OK or max rounds being reached.
func (e *ChatEngine) runRound(ctx context.Context, room *Room) (bool, error) {
	snap := room.Snapshot()
	round := snap.CurrentRound // 0-based internal round number
	participants := snap.Participants

	log.Printf("room %s: round %d/%d", room.ID, round+1, snap.MaxRounds)

	// Build transcript from previous round messages (empty on round 1).
	transcript := buildTranscript(room, round, participants)

	var wg sync.WaitGroup
	var errOnce sync.Once
	var roundErr error
	var nonOK atomic.Int32

	for i, p := range participants {
		wg.Add(1)
		go func(idx int, part Participant) {
			defer wg.Done()
			var resp string
			if part.IsExternal() {
				// Wait for external participant to send via HTTP endpoint.
				timeout := e.adjunctTimeout
				if timeout <= 0 {
					timeout = 5 * time.Minute
				}
				select {
				case <-part.ready:
					var ok bool
					resp, ok = e.store.GetExternalResponse(room.ID, part.Name)
					if !ok || resp == "" {
						resp = fmt.Sprintf("(timeout: %s)", part.Name)
					}
				case <-time.After(timeout):
					resp = fmt.Sprintf("(timeout: %s)", part.Name)
				case <-ctx.Done():
					resp = fmt.Sprintf("(cancelled: %s)", part.Name)
				}
			} else {
				text := transcript
				if round == 0 {
					// In round 1 we already sent onboarding; just ask for the first contribution.
					text = fmt.Sprintf("%s\n\nYour turn, %s. Write your first message.", buildHeader(room.Topic, participants), part.Name)
				} else {
					text = fmt.Sprintf("%s\n\nYour turn, %s. Write your response (or \"OK\" if nothing to say).", transcript, part.Name)
				}
				var err error
				resp, err = e.agent.SendMessage(part.SessionID, text, part.Agent, part.Model)
				if err != nil {
					errOnce.Do(func() { roundErr = fmt.Errorf("%s: %w", part.Name, err) })
					resp = fmt.Sprintf("(timeout: %s)", err)
				}
			}
			resp = strings.TrimSpace(resp)
			msg := Message{
				Round:     round,
				From:      part.Name,
				Text:      resp,
				Timestamp: time.Now(),
			}
			if err := e.store.AddMessage(room.ID, msg); err != nil {
				log.Printf("room %s: add message error: %v", room.ID, err)
			}
			if e.logger != nil {
				_ = e.logger.LogMessage(room.ID, round, part.Name, resp, time.Now())
			}
			if !IsOKResponse(resp) {
				nonOK.Add(1)
			}
		}(i, p)
	}

	wg.Wait()
	if roundErr != nil {
		// Save timeout markers but continue.
		log.Printf("room %s: round %d had participant error(s): %v", room.ID, round+1, roundErr)
	}

	allOK := nonOK.Load() == 0

	// If a human adjunct is joined, wait for them to respond before
	// finishing the round. The human message is injected via chat_send
	// (AddExternalMessage), which appends it to the transcript and signals
	// the humanReady channel. If they don't respond within the timeout,
	// record a timeout marker and continue.
	if snap.AdjunctName != "" {
		ch := room.GetHumanReady()
		if ch != nil {
			timeout := e.adjunctTimeout
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			select {
			case _, ok := <-ch:
				if !ok {
					// Adjunct left while we were waiting.
					log.Printf("room %s: round %d adjunct left while waiting", room.ID, round+1)
				}
			case <-time.After(timeout):
				timeoutMsg := "(adjunct timeout)"
				log.Printf("room %s: round %d adjunct timeout", room.ID, round+1)
				if err := e.store.AddMessage(room.ID, Message{
					Round:     round,
					From:      snap.AdjunctName,
					Text:      timeoutMsg,
					Timestamp: time.Now(),
				}); err != nil {
					return false, err
				}
				if e.logger != nil {
					_ = e.logger.LogMessage(room.ID, round, snap.AdjunctName, timeoutMsg, time.Now())
				}
			case <-ctx.Done():
				// Room was stopped while waiting for the adjunct.
				log.Printf("room %s: round %d adjunct wait cancelled", room.ID, round+1)
				return false, ctx.Err()
			}
		}
	}

	// Mark round as complete — everyone (AI + human) has now spoken.
	completedRound := room.IncrementCurrentRound()

	// If max rounds were reached and the last thing said was a real human
	// adjunct message, give the AI(s) a chance to reply to that final message
	// before stopping. Without this, the human's last message would be lost
	// (issue #7). The synthetic timeout marker is ignored by
	// lastMessageIsFromAdjunct.
	if completedRound >= snap.MaxRounds && snap.AdjunctName != "" &&
		lastMessageIsFromAdjunct(room, snap.AdjunctName) {
		log.Printf("room %s: max rounds reached with pending human message, running final reply", room.ID)
		if err := e.runFinalReply(ctx, room, round, participants); err != nil {
			return false, err
		}
		return true, nil
	}

	if completedRound >= snap.MaxRounds {
		log.Printf("room %s: round %d complete, max rounds (%d) reached",
			room.ID, completedRound, snap.MaxRounds)
		return true, nil
	}

	stop := room.UpdateOKRounds(allOK)
	if !stop {
		time.Sleep(e.roundDelay)
	}

	return stop, nil
}

// runFinalReply sends one more AI response round after max_rounds has been
// reached, so the final human message is not left without an answer. The
// round counter is not incremented — this reply is an extension of the
// round that just completed.
func (e *ChatEngine) runFinalReply(ctx context.Context, room *Room, round int, participants []Participant) error {
	log.Printf("room %s: final reply round %d/%d", room.ID, round+1, room.GetMaxRounds())

	// Build transcript that includes the human's last message (round `round`)
	// so the AI can reply in context.
	transcript := buildTranscriptForRounds(room, round, participants, []int{round})

	// Only non-external AI participants take part in the final reply round.
	// External participants are ignored: they have no opencode session and must
	// not receive synthetic transcript entries.
	aiParticipants := make([]Participant, 0, len(participants))
	for _, p := range participants {
		if !p.IsExternal() {
			aiParticipants = append(aiParticipants, p)
		}
	}
	if len(aiParticipants) == 0 {
		log.Printf("room %s: final reply round has no AI participants", room.ID)
		return nil
	}

	var wg sync.WaitGroup
	var errOnce sync.Once
	var roundErr error

	for _, p := range aiParticipants {
		wg.Add(1)
		go func(part Participant) {
			defer wg.Done()
			text := fmt.Sprintf("%s\n\nYour turn, %s. Write your response (or \"OK\" if nothing to say).", transcript, part.Name)
			resp, err := e.agent.SendMessage(part.SessionID, text, part.Agent, part.Model)
			if err != nil {
				errOnce.Do(func() { roundErr = fmt.Errorf("%s: %w", part.Name, err) })
				resp = fmt.Sprintf("(timeout: %s)", err)
			}
			resp = strings.TrimSpace(resp)
			msg := Message{
				Round:     round,
				From:      part.Name,
				Text:      resp,
				Timestamp: time.Now(),
			}
			if err := e.store.AddMessage(room.ID, msg); err != nil {
				log.Printf("room %s: final reply add message error: %v", room.ID, err)
			}
			if e.logger != nil {
				_ = e.logger.LogMessage(room.ID, round, part.Name, resp, time.Now())
			}
		}(p)
	}

	wg.Wait()
	if roundErr != nil {
		log.Printf("room %s: final reply round %d had participant error(s): %v", room.ID, round+1, roundErr)
	}

	time.Sleep(e.roundDelay)
	return nil
}

// finishRoom stops the room and collects summary.
func (e *ChatEngine) finishRoom(room *Room, cause error) {
	_ = e.store.SetStatus(room.ID, RoomStopped)

	e.mu.Lock()
	delete(e.active, room.ID)
	delete(e.completions, room.ID)
	e.mu.Unlock()

	if cause != nil && cause != context.Canceled {
		log.Printf("room %s: finished with error: %v", room.ID, cause)
	} else {
		log.Printf("room %s: finished after %d round(s)", room.ID, room.CurrentRound)
	}

	// Collect summaries and store digest on the room. The digest is returned
	// by chat_stop / chat_get regardless of whether a SummarySaver is set.
	// Auto-save to memory-store-mcp is opt-in via SetSummaryAutoSave.
	summaries, err := e.collectSummaries(room)
	if err != nil {
		log.Printf("room %s: summary collection error: %v", room.ID, err)
	} else {
		digest := buildDigest(room, summaries)
		if err := e.store.SetDigest(room.ID, &digest); err != nil {
			log.Printf("room %s: store digest error: %v", room.ID, err)
		}
		if e.summaryAutoSave && e.summary != nil {
			if err := e.summary.Save(room.ID, digest); err != nil {
				log.Printf("room %s: summary save error: %v", room.ID, err)
			} else {
				log.Printf("room %s: summary saved", room.ID)
			}
		}
	}

	// Close opencode sessions.
	e.closeSessions(room)
}

// buildDigest assembles the unified summary digest.
func buildDigest(room *Room, summaries []Summary) Digest {
	d := Digest{
		RoomID:       room.ID,
		Topic:        room.Topic,
		Participants: make([]string, 0, len(room.Participants)),
		Rounds:       room.CurrentRound,
		CreatedAt:    room.CreatedAt,
		Summaries:    summaries,
	}
	room.mu.RLock()
	defer room.mu.RUnlock()
	for _, p := range room.Participants {
		d.Participants = append(d.Participants, p.Name)
	}
	if room.StoppedAt != nil {
		d.StoppedAt = *room.StoppedAt
	}
	return d
}

// closeSessions closes all opencode sessions for a room.
func (e *ChatEngine) closeSessions(room *Room) {
	room.mu.RLock()
	participants := make([]Participant, len(room.Participants))
	copy(participants, room.Participants)
	room.mu.RUnlock()
	for _, p := range participants {
		if p.IsExternal() || p.SessionID == "" {
			continue
		}
		// Explicitly delete the session on the opencode-serve side.
		_ = e.agent.CloseSession(p.SessionID)
	}
}

// RoundlessSend handles a send in roundless mode: adds the external message,
// triggers all AI participants immediately, and returns the response text.
func (e *ChatEngine) RoundlessSend(roomID, senderName, message string) (string, error) {
	// Add external message to both externalResps (for signaling) and Messages (for transcript).
	if err := e.store.AddExternalParticipantMessage(roomID, senderName, message); err != nil {
		return "", fmt.Errorf("add message: %w", err)
	}
	// In roundless mode, the engine's run loop doesn't pick up externalResps,
	// so we append the message directly to Messages here.
	msg := Message{
		Round:     0,
		From:      senderName,
		Text:      message,
		Timestamp: time.Now(),
	}
	if err := e.store.AddMessage(roomID, msg); err != nil {
		return "", fmt.Errorf("add external message: %w", err)
	}

	return e.triggerAIParticipants(roomID)
}

// RoundlessSendAI triggers AI participants in roundless mode without adding
// a new external message. Used after batch-adding multiple messages via Messages array.
func (e *ChatEngine) RoundlessSendAI(roomID string) (string, error) {
	return e.triggerAIParticipants(roomID)
}

// triggerAIParticipants sends new (delta) messages since last trigger to all AI
// participants and collects their responses with a 20-second timeout per AI.
func (e *ChatEngine) triggerAIParticipants(roomID string) (string, error) {
	// Find AI participants and collect their sessions.
	snap := e.store.GetRoom(roomID)
	if snap == nil {
		return "", fmt.Errorf("room not found")
	}
	snap.mu.RLock()
	participants := make([]Participant, len(snap.Participants))
	copy(participants, snap.Participants)
	totalMsgs := len(snap.Messages)
	from := snap.lastTriggerMsgCount
	if from < 0 {
		from = 0
	}
	newMsgs := make([]Message, 0, totalMsgs-from)
	for i := from; i < totalMsgs; i++ {
		newMsgs = append(newMsgs, snap.Messages[i])
	}
	currentRound := snap.CurrentRound
	snap.mu.RUnlock()

	// Build transcript from new messages only (session history covers the rest).
	var transcript string
	if from == 0 && totalMsgs > 0 {
		// First trigger: include header with full context.
		transcript = buildTranscriptFromMessages(newMsgs, participants)
	} else if len(newMsgs) > 0 {
		// Delta trigger: label as new messages.
		var b strings.Builder
		b.WriteString("New messages:\n")
		for _, m := range newMsgs {
			if m.From != "" {
				fmt.Fprintf(&b, "%s: %s\n", m.From, m.Text)
			}
		}
		transcript = b.String()
	} else {
		transcript = ""
	}

	// Update last trigger count.
	if room := e.store.GetRoom(roomID); room != nil {
		room.mu.Lock()
		room.lastTriggerMsgCount = totalMsgs
		room.mu.Unlock()
	}

	// Send to each AI participant and collect responses with 20s timeout.
	var responses []string
	timeout := 20 * time.Second
	for _, p := range participants {
		if p.IsExternal() || p.SessionID == "" {
			continue
		}
		text := fmt.Sprintf("%s\n\nYour turn, %s. Write your response.", transcript, p.Name)

		type aiResult struct {
			resp string
			err  error
		}
		ch := make(chan aiResult, 1)
		go func(sid, txt, ag, mdl string) {
			r, e := e.agent.SendMessage(sid, txt, ag, mdl)
			ch <- aiResult{r, e}
		}(p.SessionID, text, p.Agent, p.Model)

		var resp string
		select {
		case result := <-ch:
			if result.err != nil {
				// SendMessage error (e.g. unknown agent in opencode-serve).
				// Log and skip — don't pollute the transcript with error messages.
				log.Printf("room %s: %s send error: %v", roomID, p.Name, result.err)
				continue
			}
			resp = strings.TrimSpace(result.resp)
		case <-time.After(timeout):
			log.Printf("room %s: %s timeout after %v", roomID, p.Name, timeout)
			// Collect late response in background so it isn't lost.
			go func(name, sid string, round int, c chan aiResult) {
				select {
				case result := <-c:
					if result.err == nil {
						r := strings.TrimSpace(result.resp)
						if r != "" {
							msg := Message{Round: round, From: name, Text: r, Timestamp: time.Now()}
							if err := e.store.AddMessage(roomID, msg); err != nil {
								log.Printf("room %s: late add message error: %v", roomID, err)
							}
							if e.logger != nil {
								_ = e.logger.LogMessage(roomID, round, name, r, time.Now())
							}
							log.Printf("room %s: %s late response collected after timeout", roomID, name)
						}
					}
				case <-time.After(30 * time.Second):
					log.Printf("room %s: %s late response timed out after 30s grace", roomID, name)
				}
			}(p.Name, p.SessionID, currentRound, ch)
			continue
		}

		// Add AI response to room transcript.
		msg := Message{
			Round:     currentRound,
			From:      p.Name,
			Text:      resp,
			Timestamp: time.Now(),
		}
		if err := e.store.AddMessage(roomID, msg); err != nil {
			log.Printf("room %s: add message error: %v", roomID, err)
		}
		if e.logger != nil {
			_ = e.logger.LogMessage(roomID, currentRound, p.Name, resp, time.Now())
		}
		responses = append(responses, resp)
	}
	return strings.Join(responses, "\n"), nil
}

// collectSummaries asks each participant for a summary.
func (e *ChatEngine) collectSummaries(room *Room) ([]Summary, error) {
	room.mu.RLock()
	participants := make([]Participant, len(room.Participants))
	copy(participants, room.Participants)
	room.mu.RUnlock()

	prompt := `The chat is finished. Please write a short summary:
- What did you learn?
- What ideas/insights emerged?
- What questions remain open?`

	var summaries []Summary
	for _, p := range participants {
		if p.IsExternal() || p.SessionID == "" {
			continue
		}
		text, err := e.agent.SendMessage(p.SessionID, prompt, p.Agent, p.Model)
		if err != nil {
			text = fmt.Sprintf("(summary error: %s)", err)
		}
		summaries = append(summaries, Summary{From: p.Name, Text: text})
	}
	return summaries, nil
}

// buildTranscriptFromMessages creates a transcript string from a message list.
func buildTranscriptFromMessages(msgs []Message, participants []Participant) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Conversation so far:\n")
	for _, m := range msgs {
		if m.From == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", m.From, m.Text)
	}
	return b.String()
}

// buildOnboarding creates the initial system-style message for each participant.
func buildOnboarding(topic string, participants []Participant) string {
	names := make([]string, len(participants))
	for i, p := range participants {
		names[i] = p.Name
	}
	n := len(participants)
	var rules string
	if n == 2 {
		rules = "There are 2 participants. Respond to every message."
	} else {
		rules = fmt.Sprintf("There are %d participants. Respond if you have something to say, otherwise reply with exactly \"OK\" to skip your turn.", n)
	}
	return fmt.Sprintf("You are in a chat. Topic: %q.\nParticipants: %s.\n\n%s\n\nImportant rules:\n- Answer EVERY question in each message, do not skip any.\n- If you discover an interesting fact, insight, or useful knowledge during the conversation, save it to memory via memory_save immediately.\n- At the end of the chat, you will be asked for a summary.", topic, strings.Join(names, ", "), rules)
}

// buildHeader returns the participant header for a round prompt.
func buildHeader(topic string, participants []Participant) string {
	names := make([]string, len(participants))
	for i, p := range participants {
		names[i] = p.Name
	}
	return fmt.Sprintf("You are in a chat. Topic: %q.\nParticipants: %s (total: %d).", topic, strings.Join(names, ", "), len(participants))
}

// buildTranscript builds the previous-round transcript for distribution.
func buildTranscript(room *Room, round int, participants []Participant) string {
	return buildTranscriptForRounds(room, round, participants, []int{round - 1})
}

// buildTranscriptForRounds builds a transcript from the specified round numbers.
func buildTranscriptForRounds(room *Room, round int, participants []Participant, rounds []int) string {
	var sb strings.Builder
	sb.WriteString(buildHeader(room.Topic, participants))
	sb.WriteString(fmt.Sprintf("\n\n[Round %d/%d]\n", round+1, room.MaxRounds))

	var messages []Message
	for _, r := range rounds {
		messages = append(messages, room.MessagesForRound(r)...)
	}
	if len(messages) == 0 {
		sb.WriteString("(no previous messages)\n")
	} else {
		sb.WriteString("Messages from participants:\n")
		for _, m := range messages {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", m.From, m.Text))
		}
	}
	return sb.String()
}

// lastMessageIsFromAdjunct reports whether the most recent message in the room
// transcript was sent by the human adjunct, excluding the synthetic timeout
// marker which is not a real human message.
func lastMessageIsFromAdjunct(room *Room, adjunctName string) bool {
	room.mu.RLock()
	defer room.mu.RUnlock()
	if len(room.Messages) == 0 {
		return false
	}
	last := room.Messages[len(room.Messages)-1]
	return last.From == adjunctName && last.Text != "(adjunct timeout)"
}

// AskAgent sends a single synchronous question to an opencode-serve agent
// and returns its answer. It creates an ephemeral session, sends the question
// through the blocking SendMessage endpoint, then explicitly closes the session
// via DELETE /session/{id}. The session is not stored in the RoomStore and no
// room is created.
func (e *ChatEngine) AskAgent(agentName, question, model string) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("agent is required")
	}
	if question == "" {
		return "", fmt.Errorf("question is required")
	}

	denyPerms := []PermissionRule{
		{Prefix: "chat-machines*", Deny: true},
	}
	sessionID, err := e.agent.CreateSession("ask-"+agentName, agentName, model, "/tmp", denyPerms)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	answer, err := e.agent.SendMessage(sessionID, question, agentName, model)
	if err != nil {
		// Still try to close the session; ignore close errors.
		_ = e.agent.CloseSession(sessionID)
		return "", fmt.Errorf("send message: %w", err)
	}

	_ = e.agent.CloseSession(sessionID)
	answer = strings.TrimSpace(answer)

	// Save log to memory-store-mcp (async, best-effort).
	go e.saveAskAgentLog(agentName, question, answer)

	return answer, nil
}

// ParticipantConfig is the user-provided configuration for a participant.
type ParticipantConfig struct {
	Name      string `json:"name"`
	Agent     string `json:"agent,omitempty"`
	Model     string `json:"model,omitempty"`
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	HumanType string `json:"human_type,omitempty"`
}

// Summary is a single participant's summary.
type Summary struct {
	From string `json:"from"`
	Text string `json:"text"`
}

// SummarySaver persists the final chat digest.
type SummarySaver interface {
	Save(roomID string, digest Digest) error
}
