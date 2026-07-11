// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func newExternalTestEngine() (*ChatEngine, string, string, string) {
	client := newMockAgentClient(map[string]string{
		"ses_Baron": "hello from Baron",
	})
	engine := NewChatEngine(client)
	engine.SetRoundDelay(10 * time.Millisecond)

	roomID, err := engine.CreateChatRoom("external http test", []ParticipantConfig{
		{Name: "Baron"},
		{Name: "Lena", Type: "external", Role: "participant", HumanType: "human+agent"},
	}, 1, "")
	if err != nil {
		panic(err)
	}

	r := engine.GetRoom(roomID)
	r.mu.RLock()
	var token string
	for _, p := range r.Participants {
		if p.Name == "Lena" {
			token = p.token
			break
		}
	}
	r.mu.RUnlock()
	return engine, roomID, token, "https://example.com"
}

func buildExternalURL(base, roomID, token, action, message string) string {
	body := fmt.Sprintf(`{"action":"%s","message":%q}`, action, message)
	return fmt.Sprintf("%s/room/%s/external?token=%s&body=%s", base, roomID, token, url.QueryEscape(body))
}

func TestExternalHandlerConnect(t *testing.T) {
	engine, roomID, token, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	// Wait for AI response and engine to enter wait for external.
	time.Sleep(200 * time.Millisecond)

	handler := ExternalHandler(engine, baseURL)
	req := httptest.NewRequest(http.MethodGet, buildExternalURL(baseURL, roomID, token, "connect", ""), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp ExternalEndpointResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Room.ID != roomID {
		t.Fatalf("expected room %s, got %s", roomID, resp.Room.ID)
	}
	if resp.Prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if resp.Skill == "" {
		t.Fatal("expected skill for human+agent participant")
	}
}

func TestExternalHandlerSend(t *testing.T) {
	engine, roomID, token, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	time.Sleep(200 * time.Millisecond)

	handler := ExternalHandler(engine, baseURL)
	req := httptest.NewRequest(http.MethodGet, buildExternalURL(baseURL, roomID, token, "send", "hi from Lena"), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Wait for the round to finish.
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

	found := false
	for _, m := range snap.Messages {
		if m.From == "Lena" && m.Text == "hi from Lena" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Lena message in transcript, got %v", snap.Messages)
	}
}

func TestExternalHandlerInvalidToken(t *testing.T) {
	engine, roomID, _, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	handler := ExternalHandler(engine, baseURL)
	req := httptest.NewRequest(http.MethodGet, buildExternalURL(baseURL, roomID, "bad-token", "connect", ""), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExternalHandlerMissingToken(t *testing.T) {
	engine, roomID, _, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	handler := ExternalHandler(engine, baseURL)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s/room/%s/external?body=connect", baseURL, roomID), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExternalHandlerPostNotAllowed(t *testing.T) {
	engine, _, token, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	handler := ExternalHandler(engine, baseURL)
	// room id is not important for method check, but must parse.
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("%s/room/any/external?token=%s&body=connect", baseURL, token), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExternalHandlerSendRequiresMessage(t *testing.T) {
	engine, roomID, token, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	handler := ExternalHandler(engine, baseURL)
	req := httptest.NewRequest(http.MethodGet, buildExternalURL(baseURL, roomID, token, "send", ""), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExternalHandlerSince(t *testing.T) {
	engine, roomID, token, baseURL := newExternalTestEngine()
	defer engine.StopAll()

	time.Sleep(200 * time.Millisecond)

	// Get total message count before test.
	snap, _ := engine.GetRoomSnapshot(roomID)
	totalBefore := len(snap.Messages)

	handler := ExternalHandler(engine, baseURL)
	bodySince0 := fmt.Sprintf(`{"action":"get","since":0}`)
	bodySinceLarge := fmt.Sprintf(`{"action":"get","since":%d}`, totalBefore+1)

	for _, tc := range []struct {
		name           string
		body           string
		wantMsgs       int
		wantNextSince  int
	}{
		{"since=0 returns all", bodySince0, totalBefore, totalBefore},
		{"since=total+1 returns empty", bodySinceLarge, 0, totalBefore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := fmt.Sprintf("%s/room/%s/external?token=%s&body=%s", baseURL, roomID, token, url.QueryEscape(tc.body))
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
			}

			var resp ExternalEndpointResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			if resp.NextSince != tc.wantNextSince {
				t.Fatalf("next_since: want %d, got %d", tc.wantNextSince, resp.NextSince)
			}
			if len(resp.Room.Messages) != tc.wantMsgs {
				t.Fatalf("room.messages len: want %d, got %d", tc.wantMsgs, len(resp.Room.Messages))
			}
			totalRounds := 0
			for _, g := range resp.Rounds {
				totalRounds += len(g.Messages)
			}
			if totalRounds != tc.wantMsgs {
				t.Fatalf("rounds total: want %d, got %d", tc.wantMsgs, totalRounds)
			}
		})
	}
}

func TestBuildRoundGroups(t *testing.T) {
	msgs := []Message{
		{Round: 0, From: "A", Text: "a"},
		{Round: 0, From: "B", Text: "b"},
		{Round: 1, From: "A", Text: "a2"},
	}
	groups := buildRoundGroups(msgs)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups[0].Messages) != 2 {
		t.Fatalf("expected 2 messages in round 0, got %d", len(groups[0].Messages))
	}
	if len(groups[1].Messages) != 1 {
		t.Fatalf("expected 1 message in round 1, got %d", len(groups[1].Messages))
	}
}

func TestExtractRoomID(t *testing.T) {
	cases := []struct {
		path    string
		wantID  string
		wantOK  bool
	}{
		{"/room/abc/external", "abc", true},
		{"/room/abc/external/", "abc", true},
		{"/room/abc/external/extra", "abc", true},
		{"/room/abc", "", false},
		{"/other/abc/external", "", false},
	}
	for _, c := range cases {
		id, ok := extractRoomID(c.path)
		if ok != c.wantOK || id != c.wantID {
			t.Errorf("extractRoomID(%q) = (%q, %v), want (%q, %v)", c.path, id, ok, c.wantID, c.wantOK)
		}
	}
}

// compile-time guard: ensure NewMCPServer signature is used in tests.
var _ = NewMCPServer
