// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentClientCreateSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SessionInfo{ID: "ses_test123", Agent: "Baron"})
	}))
	defer ts.Close()

	c := NewAgentClient(ts.URL, 5*time.Second)
	sid, err := c.CreateSession("test", "Baron", "", "", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sid != "ses_test123" {
		t.Fatalf("expected ses_test123, got %s", sid)
	}
}

func TestAgentClientCreateSessionWithPermissions(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SessionInfo{ID: "ses_perm456", Agent: "Baron"})
	}))
	defer ts.Close()

	c := NewAgentClient(ts.URL, 5*time.Second)
	perms := []PermissionRule{{Prefix: "chat-machines*", Deny: true}}
	sid, err := c.CreateSession("test", "Baron", "", "", perms)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sid != "ses_perm456" {
		t.Fatalf("expected ses_perm456, got %s", sid)
	}

	rawPerms, ok := gotBody["permissions"]
	if !ok {
		t.Fatalf("expected permissions in request body, got %v", gotBody)
	}
	permsList, ok := rawPerms.([]any)
	if !ok || len(permsList) != 1 {
		t.Fatalf("expected 1 permission rule, got %v", rawPerms)
	}
	first, ok := permsList[0].(map[string]any)
	if !ok {
		t.Fatalf("expected permission object, got %T", permsList[0])
	}
	if first["prefix"] != "chat-machines*" {
		t.Fatalf("expected prefix chat-machines*, got %v", first["prefix"])
	}
	if first["deny"] != true {
		t.Fatalf("expected deny=true, got %v", first["deny"])
	}
}

func TestAgentClientSendMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session/sid/message" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		parts := body["parts"].([]any)
		text := parts[0].(map[string]any)["text"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messageResponse{
			Info: struct {
				ID        string `json:"id"`
				SessionID string `json:"sessionID"`
			}{ID: "msg_1", SessionID: "sid"},
			Parts: []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			}{{Type: "text", Text: "Echo: " + text}},
		})
	}))
	defer ts.Close()

	c := NewAgentClient(ts.URL, 5*time.Second)
	resp, err := c.SendMessage("sid", "hello", "Baron", "")
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if !strings.Contains(resp, "hello") {
		t.Fatalf("expected echo of hello, got %q", resp)
	}
}

func TestAgentClientGetLastResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/session/sid/message" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]messageResponse{
			{
				Info: struct {
					ID        string `json:"id"`
					SessionID string `json:"sessionID"`
				}{ID: "msg_1", SessionID: "sid"},
				Parts: []struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}{{Type: "text", Text: "last"}},
			},
		})
	}))
	defer ts.Close()

	c := NewAgentClient(ts.URL, 5*time.Second)
	text, found, err := c.GetLastResponse("sid")
	if err != nil {
		t.Fatalf("get last response: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if text != "last" {
		t.Fatalf("expected last, got %q", text)
	}
}

func TestAgentClientCloseSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/session/sid" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c := NewAgentClient(ts.URL, 5*time.Second)
	if err := c.CloseSession("sid"); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestSplitModel(t *testing.T) {
	cases := []struct {
		in           string
		wantProvider string
		wantModel    string
	}{
		{"ollama-cloud/deepseek-v4-flash", "ollama-cloud", "deepseek-v4-flash"},
		{"deepseek-v4-flash", "", "deepseek-v4-flash"},
	}
	for _, tc := range cases {
		p, m := splitModel(tc.in)
		if p != tc.wantProvider || m != tc.wantModel {
			t.Fatalf("splitModel(%q) = (%q, %q), want (%q, %q)", tc.in, p, m, tc.wantProvider, tc.wantModel)
		}
	}
}
