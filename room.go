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

// consecutiveOKStop is the number of consecutive all-OK rounds that triggers auto-stop.
const consecutiveOKStop = 2

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

// RoomSnapshot is an immutable copy of a Room for safe concurrent reads.
type RoomSnapshot struct {
	ID           string        `json:"id"`
	Topic        string        `json:"topic"`
	Participants []Participant `json:"participants"`
	MaxRounds    int           `json:"max_rounds"`
	CurrentRound int           `json:"current_round"`
	Status       RoomStatus    `json:"status"`
	Messages     []Message     `json:"messages"`
	CreatedAt    time.Time     `json:"created_at"`
	StoppedAt    *time.Time    `json:"stopped_at,omitempty"`
	OKRounds     int           `json:"ok_rounds"`
}

// Snapshot returns a deep-ish copy of the room under read lock.
// The returned snapshot is safe to use after the lock is released.
func (r *Room) Snapshot() RoomSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p := make([]Participant, len(r.Participants))
	copy(p, r.Participants)
	m := make([]Message, len(r.Messages))
	copy(m, r.Messages)
	var stopped *time.Time
	if r.StoppedAt != nil {
		t := *r.StoppedAt
		stopped = &t
	}
	return RoomSnapshot{
		ID:           r.ID,
		Topic:        r.Topic,
		Participants: p,
		MaxRounds:    r.MaxRounds,
		CurrentRound: r.CurrentRound,
		Status:       r.Status,
		Messages:     m,
		CreatedAt:    r.CreatedAt,
		StoppedAt:    stopped,
		OKRounds:     r.okRounds,
	}
}

// GetSnapshot returns a snapshot of a room by id, or false if not found.
func (s *RoomStore) GetSnapshot(id string) (RoomSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[id]
	if !ok {
		return RoomSnapshot{}, false
	}
	return r.Snapshot(), true
}

// ListSnapshots returns snapshots of all rooms.
func (s *RoomStore) ListSnapshots() []RoomSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RoomSnapshot, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r.Snapshot())
	}
	return out
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

// GetStatus returns the room status under lock.
func (r *Room) GetStatus() RoomStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Status
}

// GetMaxRounds returns max rounds under lock.
func (r *Room) GetMaxRounds() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.MaxRounds
}

// GetCurrentRound returns current round under lock.
func (r *Room) GetCurrentRound() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.CurrentRound
}

// IncrementCurrentRound increments and returns the current round under lock.
func (r *Room) IncrementCurrentRound() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.CurrentRound++
	return r.CurrentRound
}

// GetOKRounds returns consecutive OK rounds under lock.
func (r *Room) GetOKRounds() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.okRounds
}

// UpdateOKRounds updates the consecutive OK counter and returns whether stop is triggered.
func (r *Room) UpdateOKRounds(allOK bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if allOK {
		r.okRounds++
	} else {
		r.okRounds = 0
	}
	return r.okRounds >= consecutiveOKStop
}

// IsOKResponse reports whether a response is the OK skip marker.
func IsOKResponse(text string) bool {
	return text == "OK" || text == "ok" || text == "Ok"
}
