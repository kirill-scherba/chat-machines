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
	// Wait for graceful shutdown and summary collection.
	time.Sleep(300 * time.Millisecond)

	snap, ok := engine.GetRoomSnapshot(roomID)
	if !ok {
		t.Fatalf("room %s not found after stop", roomID)
	}
	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
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

func contains(s, substr string) bool { return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSub(s, substr)) }
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
