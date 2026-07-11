// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"strings"
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[sessionID] = true // mark session so the first real SendMessage is not treated as onboarding
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
	}, 3, "")
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
	}, 10, "")
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
	}, 2, "")
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
	}, 100, "")
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
	}, 100, "")
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

	r1, _ := engine.CreateChatRoom("room1", []ParticipantConfig{{Name: "X"}, {Name: "Y"}}, 5, "")
	r2, _ := engine.CreateChatRoom("room2", []ParticipantConfig{{Name: "P"}, {Name: "Q"}}, 5, "")

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
	}, 5, "")
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
	}, 10, "")
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
		if m.From == "A" && m.Round == 0 {
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
	}, 10, "")
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
	engine.SetAdjunctTimeout(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct", []ParticipantConfig{
		{Name: "A"}, {Name: "B"},
	}, 1, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Join as adjunct.
	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Send a human message to unblock the single round.
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
}

func TestAdjunctWaitBlocks(t *testing.T) {
	responses := map[string]string{"ses_A": "hello from A"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct wait", []ParticipantConfig{
		{Name: "A"},
	}, 1, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Wait until the AI has responded and the engine is waiting for the human.
	var snap RoomSnapshot
	foundAI := false
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		for _, m := range snap.Messages {
			if m.From == "A" && m.Round == 0 {
				foundAI = true
				break
			}
		}
		if foundAI {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundAI {
		t.Fatalf("expected AI message in round 1, got: %v", snap.Messages)
	}
	if snap.Status != RoomActive {
		t.Fatalf("expected room still active (waiting for human), got %s", snap.Status)
	}

	// Send the human message to unblock the round.
	if err := engine.AddExternalMessage(roomID, "Kirill", "Hi A"); err != nil {
		t.Fatalf("chat send: %v", err)
	}

	// Wait for the room to finish.
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}

	foundHuman := false
	for _, m := range snap.Messages {
		if m.From == "Kirill" && m.Text == "Hi A" {
			foundHuman = true
		}
	}
	if !foundHuman {
		t.Fatalf("expected human message, got: %v", snap.Messages)
	}
}

func TestAdjunctTimeout(t *testing.T) {
	responses := map[string]string{"ses_A": "hello from A"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	engine.SetAdjunctTimeout(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct timeout", []ParticipantConfig{
		{Name: "A"},
	}, 2, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Wait for the room to finish. Both rounds should time out without a human response.
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
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}

	foundTimeout := false
	humanMsgCount := 0
	aiMsgCount := 0
	for _, m := range snap.Messages {
		if m.Text == "(adjunct timeout)" {
			foundTimeout = true
		}
		if m.From == "Kirill" && m.Text != "(adjunct timeout)" {
			humanMsgCount++
		}
		if m.From == "A" {
			aiMsgCount++
		}
	}
	if !foundTimeout {
		t.Fatalf("expected timeout marker, got: %v", snap.Messages)
	}
	if humanMsgCount != 0 {
		t.Fatalf("expected no real human messages, got %d", humanMsgCount)
	}
	// Timeout markers must not be treated as a real human message, so no
	// final reply round should run and the number of AI messages must equal
	// the number of rounds.
	if aiMsgCount != 2 {
		t.Fatalf("expected 2 AI messages (one per round, no final reply after timeout), got %d", aiMsgCount)
	}
}

func TestAdjunctMultipleRounds(t *testing.T) {
	responses := map[string]string{"ses_A": "hello from A"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(20 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct rounds", []ParticipantConfig{
		{Name: "A"},
	}, 2, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Goroutine sends a human message after the AI responds in each round.
	go func() {
		sentRounds := make(map[int]bool)
		for {
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok || snap.Status == RoomStopped {
				return
			}
			for _, m := range snap.Messages {
				if m.From != "Kirill" && !sentRounds[m.Round] {
					sentRounds[m.Round] = true
					engine.AddExternalMessage(roomID, "Kirill", fmt.Sprintf("msg round %d", m.Round))
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Wait for the room to finish.
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
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
	if snap.CurrentRound != 2 {
		t.Fatalf("expected 2 rounds, got %d", snap.CurrentRound)
	}

	foundRound1 := false
	foundRound2 := false
	for _, m := range snap.Messages {
		if m.From == "Kirill" && m.Text == "msg round 0" && m.Round == 0 {
			foundRound1 = true
		}
		if m.From == "Kirill" && m.Text == "msg round 1" && m.Round == 1 {
			foundRound2 = true
		}
	}
	if !foundRound1 || !foundRound2 {
		t.Fatalf("expected human messages in both rounds, got: %v", snap.Messages)
	}
}

func TestAdjunctLeaveUnblocks(t *testing.T) {
	responses := map[string]string{"ses_A": "hello from A"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	engine.SetAdjunctTimeout(1 * time.Hour)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct leave", []ParticipantConfig{
		{Name: "A"},
	}, 2, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Wait until the AI has responded and the engine is waiting for the human.
	var snap RoomSnapshot
	foundAI := false
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		for _, m := range snap.Messages {
			if m.From == "A" && m.Round == 0 {
				foundAI = true
				break
			}
		}
		if foundAI {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundAI {
		t.Fatalf("expected AI message in round 1, got: %v", snap.Messages)
	}

	// Leave while the engine is waiting; the closed channel must unblock it.
	if err := engine.ClearAdjunct(roomID); err != nil {
		t.Fatalf("leave adjunct: %v", err)
	}

	// Wait for the room to finish.
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped after leave, got %s", snap.Status)
	}
}

func TestStopRoomWhileAdjunctWaiting(t *testing.T) {
	responses := map[string]string{"ses_A": "hello from A"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	engine.SetAdjunctTimeout(1 * time.Hour) // long timeout to prove StopRoom is fast
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("stop while waiting", []ParticipantConfig{
		{Name: "A"},
	}, 100, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Wait until the AI has responded and the engine is waiting for the human.
	foundAI := false
	for i := 0; i < 50; i++ {
		snap, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		for _, m := range snap.Messages {
			if m.From == "A" && m.Round == 0 {
				foundAI = true
				break
			}
		}
		if foundAI {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundAI {
		t.Fatalf("expected AI message in round 1")
	}

	// StopRoom must return promptly even though the engine is waiting for the adjunct.
	start := time.Now()
	if err := engine.StopRoom(roomID); err != nil {
		t.Fatalf("stop room: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("StopRoom took too long while adjunct was waiting: %v", elapsed)
	}

	snap, _ := engine.GetRoomSnapshot(roomID)
	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}
}

func TestAdjunctRejoinUnblocksWaiter(t *testing.T) {
	responses := map[string]string{"ses_A": "hello from A"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	engine.SetAdjunctTimeout(1 * time.Hour)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("adjunct rejoin", []ParticipantConfig{
		{Name: "A"},
	}, 2, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Wait until the engine is waiting for Kirill in round 1.
	foundAI := false
	for i := 0; i < 50; i++ {
		snap, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room %s not found", roomID)
		}
		for _, m := range snap.Messages {
			if m.From == "A" && m.Round == 0 {
				foundAI = true
				break
			}
		}
		if foundAI {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundAI {
		t.Fatalf("expected AI message in round 1")
	}

	// Rejoin with a different name/session. The old channel must be closed so
	// the engine doesn't hang, and the new adjunct must be able to respond.
	if err := engine.SetAdjunct(roomID, "ses_Baron", "Baron"); err != nil {
		t.Fatalf("rejoin adjunct: %v", err)
	}

	if err := engine.AddExternalMessage(roomID, "Baron", "Hi from Baron"); err != nil {
		t.Fatalf("chat send from new adjunct: %v", err)
	}

	// Wait for the room to finish.
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
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", snap.Status)
	}

	foundBaron := false
	for _, m := range snap.Messages {
		if m.From == "Baron" && m.Text == "Hi from Baron" {
			foundBaron = true
		}
	}
	if !foundBaron {
		t.Fatalf("expected message from rejoined adjunct Baron, got: %v", snap.Messages)
	}
}

func TestHumanLastMessageNotLost(t *testing.T) {
	// Reproduces issue #7: max_rounds=3, human sends message in every round.
	// AI MUST reply to the human's round-3 message before the room stops.
	responses := map[string]string{"ses_A": "hello from AI"}
	client := newMockAgentClient(responses)
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("human last msg", []ParticipantConfig{
		{Name: "A"},
	}, 3, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}
	if err := engine.SetAdjunct(roomID, "ses_Kirill", "Kirill"); err != nil {
		t.Fatalf("join adjunct: %v", err)
	}

	// Respond to every AI message to ensure all rounds complete.
	go func() {
		sent := make(map[int]bool)
		for {
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok || snap.Status == RoomStopped {
				return
			}
			for _, m := range snap.Messages {
				if m.From == "A" && !sent[m.Round] {
					sent[m.Round] = true
					engine.AddExternalMessage(roomID, "Kirill", fmt.Sprintf("reply r%d", m.Round))
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Wait for stop.
	var snap RoomSnapshot
	for i := 0; i < 200; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatal("room gone")
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snap.Status != RoomStopped {
		t.Fatal("expected stopped")
	}
	if snap.CurrentRound != 3 {
		t.Fatalf("expected 3 rounds, got %d", snap.CurrentRound)
	}

	// Verify: the human got a reply to each of their 3 messages.
	// In the AI-first model there is an initial AI opener, then one AI reply
	// per human message, so we expect 4 AI messages and 3 human messages.
	// The critical issue #7 requirement: the final transcript message must be
	// from the AI, not from the human.
	ai, human := 0, 0
	for _, m := range snap.Messages {
		if m.From == "A" {
			ai++
		}
		if m.From == "Kirill" {
			human++
		}
	}
	if human != 3 {
		t.Fatalf("expected 3 human msgs, got %d: %v", human, snap.Messages)
	}
	if ai != 4 {
		t.Fatalf("expected 4 AI msgs (opener + 3 replies), got %d: %v", ai, snap.Messages)
	}
	if len(snap.Messages) == 0 {
		t.Fatal("expected messages")
	}
	last := snap.Messages[len(snap.Messages)-1]
	if last.From != "A" {
		t.Fatalf("expected final message from AI, got %s: %v", last.From, snap.Messages)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestCreateChatRoomWithExternalParticipants verifies that external participants
// are created with tokens and URLs are generated in the chat_create response.
func TestCreateChatRoomWithExternalParticipants(t *testing.T) {
	client := newMockAgentClient(map[string]string{})
	engine := NewChatEngine(client)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("external test", []ParticipantConfig{
		{Name: "Baron"},
		{Name: "Kirill", Type: "external", Role: "controller", HumanType: "human"},
	}, 3, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	room := engine.GetRoom(roomID)
	if room == nil {
		t.Fatalf("room %s not found", roomID)
	}
	room.mu.RLock()
	defer room.mu.RUnlock()

	if len(room.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(room.Participants))
	}

	var external Participant
	var internal Participant
	for _, p := range room.Participants {
		if p.IsExternal() {
			external = p
		} else {
			internal = p
		}
	}
	if internal.Name != "Baron" {
		t.Fatalf("expected internal participant Baron, got %v", internal)
	}
	if external.Name != "Kirill" {
		t.Fatalf("expected external participant Kirill, got %v", external)
	}
	if external.token == "" {
		t.Fatal("expected external participant token")
	}
	if external.ready == nil {
		t.Fatal("expected external participant ready channel")
	}
	if room.externalChannels["Kirill"] == nil {
		t.Fatal("expected external channel registered")
	}
}

// TestRunRoundWaitsForExternalParticipant verifies the round cycle pauses until
// an external participant sends a message via AddExternalParticipantMessage.
func TestRunRoundWaitsForExternalParticipant(t *testing.T) {
	client := newMockAgentClient(map[string]string{
		"ses_Baron": "hello from Baron",
	})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("external round", []ParticipantConfig{
		{Name: "Baron"},
		{Name: "Lena", Type: "external", Role: "participant", HumanType: "human+agent"},
	}, 1, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Wait for AI to respond; external channel will be waiting.
	time.Sleep(200 * time.Millisecond)
	snap, ok := engine.GetRoomSnapshot(roomID)
	if !ok {
		t.Fatalf("room not found")
	}
	if snap.Status != RoomActive {
		t.Fatalf("expected active, got %s", snap.Status)
	}
	if snap.CurrentRound != 0 {
		t.Fatalf("expected round 0 while waiting for external, got %d", snap.CurrentRound)
	}

	// External participant sends a message.
	if err := engine.store.AddExternalParticipantMessage(roomID, "Lena", "hi everyone"); err != nil {
		t.Fatalf("add external message: %v", err)
	}

	// Wait for round to complete.
	var final RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room not found")
		}
		final = s
		if final.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if final.Status != RoomStopped {
		t.Fatalf("expected stopped, got %s", final.Status)
	}

	var baronMsgs, lenaMsgs int
	for _, m := range final.Messages {
		if m.From == "Baron" {
			baronMsgs++
		}
		if m.From == "Lena" {
			lenaMsgs++
		}
	}
	if baronMsgs == 0 {
		t.Fatalf("expected Baron messages, got %v", final.Messages)
	}
	if lenaMsgs != 1 {
		t.Fatalf("expected exactly one Lena message (no duplication), got %d: %v", lenaMsgs, final.Messages)
	}
}

// TestExternalParticipantTimeout verifies that the engine continues after an
// external participant fails to respond within the timeout.
func TestExternalParticipantTimeout(t *testing.T) {
	client := newMockAgentClient(map[string]string{})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	engine.SetAdjunctTimeout(50 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("external timeout", []ParticipantConfig{
		{Name: "A"},
		{Name: "External", Type: "external", Role: "participant", HumanType: "human"},
	}, 3, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	var snap RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room not found")
		}
		snap = s
		if snap.Status == RoomStopped {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if snap.Status != RoomStopped {
		t.Fatalf("expected stopped after timeout, got %s", snap.Status)
	}

	foundTimeout := false
	for _, m := range snap.Messages {
		if m.From == "External" && strings.Contains(m.Text, "timeout") {
			foundTimeout = true
			break
		}
	}
	if !foundTimeout {
		t.Fatalf("expected timeout marker for External, got %v", snap.Messages)
	}
}

// TestMultipleExternalParticipants verifies that multiple externals participate
// in the same round cycle.
func TestMultipleExternalParticipants(t *testing.T) {
	client := newMockAgentClient(map[string]string{
		"ses_A": "from A",
	})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("multi external", []ParticipantConfig{
		{Name: "A"},
		{Name: "X", Type: "external", Role: "participant", HumanType: "human"},
		{Name: "Y", Type: "external", Role: "participant", HumanType: "human"},
	}, 1, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	engine.store.AddExternalParticipantMessage(roomID, "X", "x message")
	time.Sleep(50 * time.Millisecond)
	engine.store.AddExternalParticipantMessage(roomID, "Y", "y message")

	var snap RoomSnapshot
	for i := 0; i < 50; i++ {
		s, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			t.Fatalf("room not found")
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

	counts := map[string]int{}
	for _, m := range snap.Messages {
		counts[m.From]++
	}
	if counts["A"] == 0 {
		t.Fatalf("expected A message, got %v", snap.Messages)
	}
	if counts["X"] != 1 {
		t.Fatalf("expected exactly one X message (no duplication), got %d: %v", counts["X"], snap.Messages)
	}
	if counts["Y"] != 1 {
		t.Fatalf("expected exactly one Y message (no duplication), got %d: %v", counts["Y"], snap.Messages)
	}
}

// TestStartRoomSkipsExternalSessions verifies no opencode sessions are created
// for external participants.
func TestStartRoomSkipsExternalSessions(t *testing.T) {
	client := newMockAgentClient(map[string]string{})
	engine := NewChatEngine(client)
	defer engine.StopAll()

	roomID, err := engine.CreateChatRoom("mixed participants", []ParticipantConfig{
		{Name: "Baron"},
		{Name: "Kirill", Type: "external", Role: "controller", HumanType: "human"},
	}, 3, "")
	if err != nil {
		t.Fatalf("create chat room: %v", err)
	}

	// Wait for the room to become active, indicating startRoom completed.
	for i := 0; i < 50; i++ {
		snap, ok := engine.GetRoomSnapshot(roomID)
		if ok && snap.Status == RoomActive {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	client.mu.Lock()
	calls := client.createCalls
	client.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 session creation (Baron only), got %d", calls)
	}
}
// TestRunFinalReplySkipsExternalParticipants verifies that runFinalReply only
// appends transcript entries for non-external AI participants and never creates
// synthetic messages for external participants.
func TestRunFinalReplySkipsExternalParticipants(t *testing.T) {
	client := newMockAgentClient(map[string]string{
		"ses_AI": "final reply from AI",
	})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(0)
	defer engine.StopAll()

	room := engine.store.CreateRoom("final reply skip", 2, "")
	room.Status = RoomActive
	room.CurrentRound = 1
	room.Participants = []Participant{
		{Name: "AI", Type: "ai", SessionID: "ses_AI"},
		{Name: "Ext", Type: "external", Role: "participant", HumanType: "human"},
	}
	room.externalChannels = map[string]chan struct{}{}
	room.externalResps = map[string]string{}
	room.Messages = []Message{
		{Round: 0, From: "AI", Text: "round 0 from AI"},
		{Round: 0, From: "Ext", Text: "round 0 from Ext"},
		{Round: 1, From: "Human", Text: "last question"},
	}

	ctx := context.Background()
	if err := engine.runFinalReply(ctx, room, 1, room.Participants); err != nil {
		t.Fatalf("runFinalReply: %v", err)
	}

	counts := map[string]int{}
	for _, m := range room.Messages {
		counts[m.From]++
	}
	if counts["AI"] != 2 {
		t.Fatalf("expected two AI messages (round 0 + final reply), got %d: %v", counts["AI"], room.Messages)
	}
	if counts["Ext"] != 1 {
		t.Fatalf("expected exactly one Ext message (round 0 only), got %d: %v", counts["Ext"], room.Messages)
	}
	if counts["Human"] != 1 {
		t.Fatalf("expected exactly one Human message, got %d: %v", counts["Human"], room.Messages)
	}
}
