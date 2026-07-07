// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Room management: chat rooms, participants, messages.
package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RoomStatus represents the lifecycle state of a chat room.
type RoomStatus string

const (
	RoomWaiting RoomStatus = "waiting"
	RoomActive  RoomStatus = "active"
	RoomStopped RoomStatus = "stopped"
)

// Participant is a single chat member backed by an opencode-serve session.
type Participant struct {
	Name      string
	Agent     string
	Model     string
	SessionID string
}

// Message is a single round entry.
type Message struct {
	Round     int       `json:"round"`
	From      string    `json:"from"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Room is a chat room with participants and a message transcript.
type Room struct {
	ID           string
	Topic        string
	Participants []Participant
	MaxRounds    int
	CurrentRound int
	Status       RoomStatus
	Messages     []Message
	CreatedAt    time.Time
	StoppedAt    *time.Time

	// okRounds tracks how many consecutive full-OK rounds happened.
	okRounds int

	mu sync.RWMutex
}

// RoomStore holds all active and completed rooms in memory.
type RoomStore struct {
	rooms map[string]*Room
	mu    sync.RWMutex
}

// NewRoomStore creates an empty store.
func NewRoomStore() *RoomStore {
	return &RoomStore{rooms: make(map[string]*Room)}
}

// CreateRoom creates a new room in waiting status.
func (s *RoomStore) CreateRoom(topic string, maxRounds int) *Room {
	if maxRounds <= 0 {
		maxRounds = 10
	}
	r := &Room{
		ID:        uuid.New().String(),
		Topic:     topic,
		MaxRounds: maxRounds,
		Status:    RoomWaiting,
		CreatedAt: time.Now(),
		Messages:  make([]Message, 0, maxRounds*4),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rooms[r.ID] = r
	return r
}

// GetRoom returns a room by id or nil if not found.
func (s *RoomStore) GetRoom(id string) *Room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rooms[id]
}

// ListRooms returns a snapshot of all rooms.
func (s *RoomStore) ListRooms() []*Room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r)
	}
	return out
}

// AddParticipant adds a participant to a room before it starts.
func (s *RoomStore) AddParticipant(roomID string, p Participant) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Status != RoomWaiting {
		return fmt.Errorf("room %s is not waiting", roomID)
	}
	r.Participants = append(r.Participants, p)
	return nil
}

// SetStatus transitions a room to a new status.
func (s *RoomStore) SetStatus(roomID string, status RoomStatus) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Status == RoomStopped {
		return fmt.Errorf("room %s already stopped", roomID)
	}
	r.Status = status
	if status == RoomStopped {
		now := time.Now()
		r.StoppedAt = &now
	}
	return nil
}

// AddMessage appends a message to a room's transcript.
func (s *RoomStore) AddMessage(roomID string, m Message) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Status == RoomStopped {
		return fmt.Errorf("room %s is stopped", roomID)
	}
	r.Messages = append(r.Messages, m)
	return nil
}

// MessagesForRound returns all messages for a specific round.
func (r *Room) MessagesForRound(round int) []Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Message
	for _, m := range r.Messages {
		if m.Round == round {
			out = append(out, m)
		}
	}
	return out
}

// ParticipantCount returns the number of participants.
func (r *Room) ParticipantCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Participants)
}

// IsOKResponse reports whether a response is the OK skip marker.
func IsOKResponse(text string) bool {
	return text == "OK" || text == "ok" || text == "Ok"
}
