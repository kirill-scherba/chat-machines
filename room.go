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

// Participant is a single chat member backed by an opencode-serve session
// or an external HTTP-based participant.
type Participant struct {
	Name      string
	Agent     string
	Model     string
	SessionID string
	Type      string // "" or "ai" = opencode-serve participant; "external" = HTTP endpoint
	Role      string // "controller" (can command) or "participant" (external only)
	HumanType string // "human" or "human+agent" (external only)

	// Internal fields, not serialized:
	token string        // UUID for external endpoint authentication
	ready chan struct{} // buffered (cap 1), signalled when external sends a message
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

	// AdjunctSessionID is the opencode-serve session ID of the human adjunct,
	// if any. Empty means no human is currently joined.
	AdjunctSessionID string
	AdjunctName      string

	// humanReady is signaled by AddExternalMessage when a human adjunct
	// sends a message via chat_send. Created by SetAdjunct (capacity 1),
	// read by runRound's select, closed by ClearAdjunct.
	humanReady chan struct{}

	// StoredDigest is the post-chat summary digest computed when the room
	// stops. It is returned by chat_stop and chat_get instead of being saved
	// directly to memory-store-mcp.
	StoredDigest *Digest

	// externalResps holds the latest message text per external participant
	// for the current round. Set by the HTTP handler, read and cleared by runRound.
	externalResps map[string]string

	// externalChannels mirrors Participant.ready for direct lookup by name
	// in the HTTP handler (which has no snapshot reference to Participants).
	externalChannels map[string]chan struct{}

	mu sync.RWMutex
}

// RoomSnapshot is an immutable copy of a Room for safe concurrent reads.
type RoomSnapshot struct {
	ID               string        `json:"id"`
	Topic            string        `json:"topic"`
	Participants     []Participant `json:"participants"`
	MaxRounds        int           `json:"max_rounds"`
	CurrentRound     int           `json:"current_round"`
	Status           RoomStatus    `json:"status"`
	Messages         []Message     `json:"messages"`
	CreatedAt        time.Time     `json:"created_at"`
	StoppedAt        *time.Time    `json:"stopped_at,omitempty"`
	OKRounds         int           `json:"ok_rounds"`
	AdjunctSessionID string        `json:"adjunct_session_id,omitempty"`
	AdjunctName      string        `json:"adjunct_name,omitempty"`
	HasAdjunct       bool          `json:"has_adjunct"`
	HasExternals     bool          `json:"has_externals"`
	StoredDigest     *Digest       `json:"digest,omitempty"`
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
	var hasExternals bool
	for _, p := range r.Participants {
		if p.IsExternal() {
			hasExternals = true
			break
		}
	}

	return RoomSnapshot{
		ID:               r.ID,
		Topic:            r.Topic,
		Participants:     p,
		MaxRounds:        r.MaxRounds,
		CurrentRound:     r.CurrentRound,
		Status:           r.Status,
		Messages:         m,
		CreatedAt:        r.CreatedAt,
		StoppedAt:        stopped,
		OKRounds:         r.okRounds,
		AdjunctSessionID: r.AdjunctSessionID,
		AdjunctName:      r.AdjunctName,
		HasAdjunct:       r.AdjunctName != "",
		HasExternals:     hasExternals,
		StoredDigest:     r.StoredDigest,
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

// AddExternalMessage injects a message from a human (adjunct) into the room
// without incrementing the round. The message becomes part of the transcript
// for the next round.
func (s *RoomStore) AddExternalMessage(roomID, from, text string) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Status == RoomStopped {
		return fmt.Errorf("room %s is stopped", roomID)
	}
	r.Messages = append(r.Messages, Message{
		Round:     r.CurrentRound,
		From:      from,
		Text:      text,
		Timestamp: time.Now(),
	})
	// Signal the engine goroutine that the human has spoken.
	if r.humanReady != nil {
		select {
		case r.humanReady <- struct{}{}:
		default: // engine already got the signal for this round
		}
	}
	return nil
}

// SetAdjunct registers an external human observer for the room.
func (s *RoomStore) SetAdjunct(roomID, sessionID, name string) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Status == RoomStopped {
		return fmt.Errorf("room %s is stopped", roomID)
	}
	r.AdjunctSessionID = sessionID
	r.AdjunctName = name
	// Replace any existing channel. Close the old one first so a waiter
	// currently blocked on it is unblocked, then create a fresh channel
	// for the new adjunct. This prevents a rejoin from stranding the
	// engine on a stale channel.
	if r.humanReady != nil {
		close(r.humanReady)
	}
	r.humanReady = make(chan struct{}, 1)
	return nil
}

// ClearAdjunct removes the human adjunct from the room.
func (s *RoomStore) ClearAdjunct(roomID string) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.humanReady != nil {
		close(r.humanReady)
		r.humanReady = nil
	}
	r.AdjunctSessionID = ""
	r.AdjunctName = ""
	return nil
}

// SetDigest stores the post-chat summary digest on the room.
func (s *RoomStore) SetDigest(roomID string, digest *Digest) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.StoredDigest = digest
	return nil
}

// AddExternalParticipantMessage stores a message from an external participant
// and signals the participant's ready channel (non-blocking). The engine is the
// sole writer of transcript entries for external participants: runRound reads
// the stored text via GetExternalResponse and appends it to the transcript.
func (s *RoomStore) AddExternalParticipantMessage(roomID, name, text string) error {
	r := s.GetRoom(roomID)
	if r == nil {
		return fmt.Errorf("room %s not found", roomID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Status == RoomStopped {
		return fmt.Errorf("room %s is stopped", roomID)
	}

	var found bool
	for _, p := range r.Participants {
		if p.Name == name && p.IsExternal() {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("external participant %q not found in room %s", name, roomID)
	}

	if r.externalResps == nil {
		r.externalResps = make(map[string]string)
	}
	if r.externalChannels == nil {
		r.externalChannels = make(map[string]chan struct{})
	}

	// Store response text for runRound to collect and record in the transcript.
	r.externalResps[name] = text

	// Signal the engine goroutine that this external has spoken.
	if ch, ok := r.externalChannels[name]; ok && ch != nil {
		select {
		case ch <- struct{}{}:
		default: // engine already got the signal for this round
		}
	}
	return nil
}

// FindExternalParticipantByToken returns the external participant matching the token.
func (s *RoomStore) FindExternalParticipantByToken(roomID, token string) (Participant, bool) {
	r := s.GetRoom(roomID)
	if r == nil {
		return Participant{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.Participants {
		if p.IsExternal() && p.token == token {
			return p, true
		}
	}
	return Participant{}, false
}

// GetExternalResponse returns and clears the latest response text for an external
// participant, or ("", false) if none is pending.
func (s *RoomStore) GetExternalResponse(roomID, name string) (string, bool) {
	r := s.GetRoom(roomID)
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.externalResps == nil {
		return "", false
	}
	text, ok := r.externalResps[name]
	if ok {
		delete(r.externalResps, name)
	}
	return text, ok
}

// RegisterExternalChannel registers or replaces the ready channel for an external
// participant. Called by the engine when creating/starting a room.
func (s *RoomStore) RegisterExternalChannel(roomID, name string, ch chan struct{}) {
	r := s.GetRoom(roomID)
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.externalChannels == nil {
		r.externalChannels = make(map[string]chan struct{})
	}
	r.externalChannels[name] = ch
}

// GetExternalChannel returns the ready channel for an external participant.
func (s *RoomStore) GetExternalChannel(roomID, name string) (chan struct{}, bool) {
	r := s.GetRoom(roomID)
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.externalChannels == nil {
		return nil, false
	}
	ch, ok := r.externalChannels[name]
	return ch, ok && ch != nil
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

// HasAdjunct reports whether a human adjunct is currently joined.
func (r *Room) HasAdjunct() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.AdjunctName != ""
}

// GetHumanReady returns the human-ready channel, or nil if no adjunct is joined.
func (r *Room) GetHumanReady() chan struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.humanReady
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

// IsExternal reports whether this participant is an external HTTP-based participant.
func (p Participant) IsExternal() bool {
	return p.Type == "external"
}

// Token returns the external participant auth token, or empty for internal participants.
func (p Participant) Token() string {
	return p.token
}

// IsOKResponse reports whether a response is the OK skip marker.
func IsOKResponse(text string) bool {
	return text == "OK" || text == "ok" || text == "Ok"
}
