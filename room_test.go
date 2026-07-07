// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"sync"
	"testing"
)

func TestRoomStoreCreateAndGet(t *testing.T) {
	s := NewRoomStore()
	r := s.CreateRoom("topic", 5)
	if r == nil || r.ID == "" {
		t.Fatal("expected room with id")
	}
	if r.Status != RoomWaiting {
		t.Fatalf("expected waiting, got %s", r.Status)
	}
	got := s.GetRoom(r.ID)
	if got != r {
		t.Fatal("expected same room pointer")
	}
}

func TestRoomStoreAddParticipant(t *testing.T) {
	s := NewRoomStore()
	r := s.CreateRoom("topic", 5)
	if err := s.AddParticipant(r.ID, Participant{Name: "Baron"}); err != nil {
		t.Fatalf("add participant: %v", err)
	}
	if err := s.AddParticipant(r.ID, Participant{Name: "Manager"}); err != nil {
		t.Fatalf("add participant: %v", err)
	}
	if r.ParticipantCount() != 2 {
		t.Fatalf("expected 2 participants, got %d", r.ParticipantCount())
	}
}

func TestRoomStoreSetStatus(t *testing.T) {
	s := NewRoomStore()
	r := s.CreateRoom("topic", 5)
	if err := s.SetStatus(r.ID, RoomActive); err != nil {
		t.Fatalf("set active: %v", err)
	}
	if r.Status != RoomActive {
		t.Fatalf("expected active, got %s", r.Status)
	}
	if err := s.SetStatus(r.ID, RoomStopped); err != nil {
		t.Fatalf("set stopped: %v", err)
	}
	// Stopping twice should fail.
	if err := s.SetStatus(r.ID, RoomStopped); err == nil {
		t.Fatal("expected error on double stop")
	}
}

func TestRoomStoreAddMessage(t *testing.T) {
	s := NewRoomStore()
	r := s.CreateRoom("topic", 5)
	if err := s.AddParticipant(r.ID, Participant{Name: "Baron"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(r.ID, RoomActive); err != nil {
		t.Fatal(err)
	}
	m := Message{Round: 1, From: "Baron", Text: "hello"}
	if err := s.AddMessage(r.ID, m); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if len(r.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(r.Messages))
	}
	if err := s.SetStatus(r.ID, RoomStopped); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(r.ID, m); err == nil {
		t.Fatal("expected error adding message to stopped room")
	}
}

func TestRoomStoreConcurrency(t *testing.T) {
	s := NewRoomStore()
	r := s.CreateRoom("topic", 5)
	for i := 0; i < 10; i++ {
		if err := s.AddParticipant(r.ID, Participant{Name: "Baron"}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.AddMessage(r.ID, Message{Round: 1, From: "Baron", Text: "msg"})
		}(i)
	}
	wg.Wait()
	if len(r.Messages) != 100 {
		t.Fatalf("expected 100 messages, got %d", len(r.Messages))
	}
}

func TestIsOKResponse(t *testing.T) {
	for _, tc := range []struct {
		text string
		ok   bool
	}{
		{"OK", true},
		{"ok", true},
		{"Ok", true},
		{"hello", false},
		{" ok ", false},
	} {
		if got := IsOKResponse(tc.text); got != tc.ok {
			t.Fatalf("IsOKResponse(%q) = %v, want %v", tc.text, got, tc.ok)
		}
	}
}
