// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"sync"
	"testing"
	"time"
)

// mockAgentClient is an AgentClient that does not make real HTTP calls.
type mockAgentClient struct {
	sessions    map[string]string // name -> session id
	responses   map[string]string // session id -> response text
	mu          sync.Mutex
	createCalls int
	sendCalls   int
}

func newMockAgentClient(responses map[string]string) *mockAgentClient {
	return &mockAgentClient{
		sessions:  make(map[string]string),
		responses: responses,
	}
}

func (m *mockAgentClient) CreateSession(name, agent, model, workDir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	sid := "ses_" + name
	m.sessions[name] = sid
	return sid, nil
}

func (m *mockAgentClient) SendMessage(sessionID, text, agent, model string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCalls++
	resp, ok := m.responses[sessionID]
	if !ok {
		return "OK", nil
	}
	return resp, nil
}

func (m *mockAgentClient) GetLastResponse(sessionID string) (string, bool, error) {
	return "", false, nil
}

func (m *mockAgentClient) SendMessageAsync(sessionID, text, agent, model string) error {
	return nil
}

// roundAwareMockAgent returns responses based on how many times each
// session has been called. Because each participant is contacted once per
// round, the call count equals the round number. It is used for OK-protocol
// tests that need different answers across rounds.
type roundAwareMockAgent struct {
	sessions    map[string]string
	responses   map[string]map[int]string // session id -> round -> response
	callCounts  map[string]int
	seen        map[string]bool
	mu          sync.Mutex
	createCalls int
	sendCalls   int
}

func newRoundAwareMockAgent(responses map[string]map[int]string) *roundAwareMockAgent {
	return &roundAwareMockAgent{
		sessions:   make(map[string]string),
		responses:  responses,
		callCounts: make(map[string]int),
		seen:       make(map[string]bool),
	}
}

func (m *roundAwareMockAgent) CreateSession(name, agent, model, workDir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	sid := "ses_" + name
	m.sessions[name] = sid
	return sid, nil
}

func (m *roundAwareMockAgent) SendMessage(sessionID, text, agent, model string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCalls++

	// The first SendMessage to a session is always the onboarding prompt.
	// Its response is discarded, so we do not count it as a round.
	if !m.seen[sessionID] {
		m.seen[sessionID] = true
		return "OK", nil
	}

	m.callCounts[sessionID]++
	round := m.callCounts[sessionID]
	if byRound, ok := m.responses[sessionID]; ok {
		if resp, ok := byRound[round]; ok {
			return resp, nil
		}
	}
	return "OK", nil
}

func (m *roundAwareMockAgent) GetLastResponse(sessionID string) (string, bool, error) {
	return "", false, nil
}

func (m *roundAwareMockAgent) SendMessageAsync(sessionID, text, agent, model string) error {
	return nil
}

func TestEngineTwoParticipants(t *testing.T) {
	responses := map[string]string{
		"ses_Baron":   "I propose we improve the architecture",
		"ses_Manager": "Agreed, let's discuss details",
	}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("project status", []ParticipantConfig{
		{Name: "Baron"},
		{Name: "Manager"},
	}, 3)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Wait for the chat to finish.
	var snap RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	if snap.CurrentRound < 1 {
		t.Fatalf("expected at least 1 round, got %d", snap.CurrentRound)
	}
	if len(snap.Messages) < 2 {
		t.Fatalf("expected messages, got %d", len(snap.Messages))
	}
}

func TestEngineAutoStopOnOK(t *testing.T) {
	// All participants always say OK.
	client := newMockAgentClient(map[string]string{})
	engine := NewChatEngine(client)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("quiet chat", []ParticipantConfig{
		{Name: "A"},
		{Name: "B"},
		{Name: "C"},
	}, 10)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	var snap RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	// Should stop at round 2 because all OK for 2 rounds.
	if snap.CurrentRound != 2 {
		t.Fatalf("expected 2 rounds (all OK), got %d", snap.CurrentRound)
	}
}

func TestEngineMaxRounds(t *testing.T) {
	responses := map[string]string{
		"ses_A": "message from A",
		"ses_B": "message from B",
	}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("max rounds", []ParticipantConfig{
		{Name: "A"},
		{Name: "B"},
	}, 2)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	var snap RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	if snap.CurrentRound != 2 {
		t.Fatalf("expected 2 rounds, got %d", snap.CurrentRound)
	}
}

func TestEngineStopRoom(t *testing.T) {
	responses := map[string]string{
		"ses_A": "A says hi",
		"ses_B": "B says hi",
	}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("manual stop", []ParticipantConfig{
		{Name: "A"},
		{Name: "B"},
	}, 100)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Wait for the goroutine to register itself, then stop.
	time.Sleep(50 * time.Millisecond)
	if err := engine.StopRoom(roomID); err != nil {
		t.Fatalf("stop room: %v", err)
	}

	snap, ok := engine.GetRoomSnapshot(roomID)
	if !ok {
		t.Fatalf("room %s not found after stop", roomID)
	}
	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
}

func TestStopRoomReturnsDigest(t *testing.T) {
	// Mock returns the same text for chat messages and summary prompts, which
	// is enough to verify that StopRoom returns a populated digest synchronously.
	responses := map[string]string{
		"ses_A": "A says hi",
		"ses_B": "B says hi",
		"ses_C": "C says hi",
	}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("digest sync", []ParticipantConfig{
		{Name: "A"},
		{Name: "B"},
		{Name: "C"},
	}, 100)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Wait until at least one round completes so sessions are created.
	for i := 0; i < 50; i++ {
		snap, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		if snap.CurrentRound >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stop the room. StopRoom now waits for finishRoom, so the digest must be
	// available immediately after it returns.
	if err := engine.StopRoom(roomID); err != nil {
		t.Fatalf("stop room: %v", err)
	}

	snap, ok := engine.GetRoomSnapshot(roomID)
	if !ok {
		t.Fatalf("room %s not found after stop", roomID)
	}
	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	if snap.StoredDigest == nil {
		t.Fatalf("expected digest after StopRoom, got nil")
	}
	if snap.StoredDigest.Topic != "digest sync" {
		t.Fatalf("expected digest topic %q, got %q", "digest sync", snap.StoredDigest.Topic)
	}
	if len(snap.StoredDigest.Summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(snap.StoredDigest.Summaries))
	}
	for _, s := range snap.StoredDigest.Summaries {
		if s.Text == "" {
			t.Fatalf("expected non-empty summary text, got %v", s)
		}
	}
}

func TestEngineStopAll(t *testing.T) {
	client := newMockAgentClient(map[string]string{})
	engine := NewChatEngine(client)
	defer engine.StopAll()

	r1, _ := engine.CreateChatRoom("room1", []ParticipantConfig{{Name: "X"}, {Name: "Y"}}, 5)
	r2, _ := engine.CreateChatRoom("room2", []ParticipantConfig{{Name: "P"}, {Name: "Q"}}, 5)

	time.Sleep(50 * time.Millisecond)
	engine.StopAll()
	time.Sleep(300 * time.Millisecond)

	s1, _ := engine.GetRoomSnapshot(r1)
	if s1.Status != RoomStopped {
		t.Fatalf("expected room1 stopped")
	}
	s2, _ := engine.GetRoomSnapshot(r2)
	if s2.Status != RoomStopped {
		t.Fatalf("expected room2 stopped")
	}
}

// TestCompletionsCleanedUpAfterNaturalFinish verifies that the engine
// removes the per-room completion channel when a room stops naturally (all OK
// auto-stop). Without this cleanup, e.completions leaks closed channels for
// every naturally completed room.
func TestCompletionsCleanedUpAfterNaturalFinish(t *testing.T) {
	client := newMockAgentClient(map[string]string{})
	engine := NewChatEngine(client)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("natural finish", []ParticipantConfig{
		{Name: "A"}, {Name: "B"},
	}, 5)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	var snap RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}

	engine.mu.Lock()
	_, exists := engine.completions[roomID]
	engine.mu.Unlock()
	if exists {
		t.Fatalf("expected completions entry for %s to be deleted after natural finish", roomID)
	}
}

func TestBuildOnboarding(t *testing.T) {
	p := []Participant{{Name: "Baron"}, {Name: "Manager"}}
	text := buildOnboarding("project", p)
	if text == "" {
		t.Fatal("expected onboarding text")
	}
	if !contains(text, "2 participants") {
		t.Fatalf("expected 2 participants rule, got %q", text)
	}
}

func TestBuildOnboardingThreePlus(t *testing.T) {
	p := []Participant{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	text := buildOnboarding("project", p)
	if !contains(text, "OK") {
		t.Fatalf("expected OK rule for 3+ participants, got %q", text)
	}
}

func TestOKProtocolMixedResponses(t *testing.T) {
	// Round 1: A contributes, B and C skip.
	// Round 2 and 3: everyone says OK → auto-stop after 2 consecutive OK rounds.
	client := newRoundAwareMockAgent(map[string]map[int]string{
		"ses_A": {1: "I have an idea", 2: "OK", 3: "OK"},
		"ses_B": {1: "OK", 2: "OK", 3: "OK"},
		"ses_C": {1: "OK", 2: "OK", 3: "OK"},
	})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("mixed ok", []ParticipantConfig{
		{Name: "A"}, {Name: "B"}, {Name: "C"},
	}, 10)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	var snap RoomSnapshot
	for i := 0; i < 80; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	// Round 1 real, rounds 2+3 OK → stop at round 3.
	if snap.CurrentRound != 3 {
		t.Fatalf("expected 3 rounds, got %d", snap.CurrentRound)
	}

	foundA := false
	for _, m := range snap.Messages {
		if m.From == "A" && m.Round == 1 {
			foundA = true
		}
	}
	if !foundA {
		t.Fatalf("expected A message in round 1, got messages: %v", snap.Messages)
	}
}

func TestOKProtocolResets(t *testing.T) {
	// Round 1: all OK (counter=1)
	// Round 2: A contributes (counter resets to 0)
	// Round 3: all OK (counter=1)
	// Round 4: all OK (counter=2 → stop)
	client := newRoundAwareMockAgent(map[string]map[int]string{
		"ses_A": {1: "OK", 2: "real message", 3: "OK", 4: "OK"},
		"ses_B": {1: "OK", 2: "OK", 3: "OK", 4: "OK"},
		"ses_C": {1: "OK", 2: "OK", 3: "OK", 4: "OK"},
	})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("ok reset", []ParticipantConfig{
		{Name: "A"}, {Name: "B"}, {Name: "C"},
	}, 10)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	var snap RoomSnapshot
	for i := 0; i < 100; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	if snap.CurrentRound != 4 {
		t.Fatalf("expected 4 rounds (1 OK + real + 2 OK), got %d", snap.CurrentRound)
	}
}

func TestEngineChatSendAndAdjunct(t *testing.T) {
	responses := map[string]string{
		"ses_A": "hello from A",
		"ses_B": "OK",
	}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct", []ParticipantConfig{
		{Name: "A"}, {Name: "B"},
	}, 3)
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Join as adjunct.
	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Send a human message.
	if err := engine.AddExternalMessage(roomID, "Kirill", "Hi agents"); err != nil {
		t.Fatalf("chat send: %v", err)
	}

	// Wait for the room to finish.
	var snap RoomSnapshot
	for i := 0; i < 80; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}

	foundHuman := false
	for _, m := range snap.Messages {
		if m.From == "Kirill" && m.Text == "Hi agents" {
			foundHuman = true
		}
	}
	if !foundHuman {
		t.Fatalf("expected human message, got: %v", snap.Messages)
	}

	if snap.AdjunctName != "Kirill" {
		t.Fatalf("expected adjunct Kirill, got %q", snap.AdjunctName)
	}

	// Leave.
	if err := engine.ClearAdjunct(roomID); err != nil {
		t.Fatalf("leave adjunct: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSub(s, substr))
}
func containsSub(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Ensure mockAgentClient implements the subset used by ChatEngine.
var _ interface {
	CreateSession(name, agent, model, workDir string) (string, error)
	SendMessage(sessionID, text, agent, model string) (string, error)
	SendMessageAsync(sessionID, text, agent, model string) error
} = (*mockAgentClient)(nil)

// compile-time guard: AgentClient methods are not used by mock directly, but we
// need to be sure the mock has the same shape. ChatEngine stores *AgentClient,
// so this test compiles only because the mock is cast to *AgentClient in a way
// that is unsafe at runtime; we handle this by converting the mock methods to
// match *AgentClient. However Go has no duck typing, so the cast will panic at
// runtime. To avoid this, we adjust the engine to accept an interface, or we
// leave engine_test with a real *AgentClient backed by a httptest server.
