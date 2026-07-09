// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// External Chat Connector (ECC) HTTP handler for non-opencode participants.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// ExternalEndpointResponse is the JSON body returned to external chat callers.
type ExternalEndpointResponse struct {
	Room   RoomSnapshot `json:"room"`
	Rounds []RoundGroup `json:"rounds"`
	Prompt string       `json:"prompt"`
	Skill  string       `json:"skill,omitempty"`
}

// RoundGroup groups messages by round for the external endpoint response.
type RoundGroup struct {
	Round    int       `json:"round"`
	Messages []Message `json:"messages"`
}

// externalActionBody is decoded from the URL-encoded JSON body parameter.
type externalActionBody struct {
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

// externalAgentSkill is returned on connect for human+agent participants.
const externalAgentSkill = `You are an AI assistant helping {name} participate in a multi-person chat room.

Room topic: {topic}
Your role: {role}

Rules:
- Read the chat transcript and help formulate responses.
- When it's {name}'s turn, write a response appropriate for the conversation.
- Reply with exactly "OK" if nothing meaningful to contribute this round.
- Be concise — other participants are waiting.

The chat-machines server sends you the current room state on every request.
Use action "send" with the message text to post {name}'s response.`

// ExternalHandler returns an http.Handler that serves the external chat endpoint.
func ExternalHandler(engine *ChatEngine, baseURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"only GET allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		roomID, ok := extractRoomID(r.URL.Path)
		if !ok {
			http.Error(w, `{"error":"invalid room path"}`, http.StatusBadRequest)
			return
		}

		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, `{"error":"token required"}`, http.StatusBadRequest)
			return
		}

		p, ok := engine.store.FindExternalParticipantByToken(roomID, token)
		if !ok {
			http.Error(w, `{"error":"invalid token"}`, http.StatusForbidden)
			return
		}

		bodyParam := r.URL.Query().Get("body")
		if bodyParam == "" {
			http.Error(w, `{"error":"body parameter required"}`, http.StatusBadRequest)
			return
		}

		var body externalActionBody
		if err := json.Unmarshal([]byte(bodyParam), &body); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid body JSON: %s"}`, err), http.StatusBadRequest)
			return
		}

		log.Printf("external: room=%s participant=%s action=%s", roomID, p.Name, body.Action)

		switch body.Action {
		case "connect":
			renderExternalResponse(w, engine, roomID, p, baseURL)
		case "send":
			if strings.TrimSpace(body.Message) == "" {
				http.Error(w, `{"error":"message is required for action=send"}`, http.StatusBadRequest)
				return
			}
			if err := engine.store.AddExternalParticipantMessage(engine.logger, roomID, p.Name, body.Message); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusConflict)
				return
			}
			renderExternalResponse(w, engine, roomID, p, baseURL)
		default:
			http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		}
	})
}

// extractRoomID parses "/room/{id}/external" or "/room/{id}/external/..." paths.
func extractRoomID(path string) (string, bool) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "room" || parts[2] != "external" {
		return "", false
	}
	return parts[1], true
}

// renderExternalResponse builds and writes the ExternalEndpointResponse.
func renderExternalResponse(w http.ResponseWriter, engine *ChatEngine, roomID string, p Participant, baseURL string) {
	snap, ok := engine.GetRoomSnapshot(roomID)
	if !ok {
		http.Error(w, `{"error":"room not found"}`, http.StatusNotFound)
		return
	}

	groups := buildRoundGroups(snap.Messages)
	prompt := buildExternalPrompt(snap, p.Name)

	resp := ExternalEndpointResponse{
		Room:   snap,
		Rounds: groups,
		Prompt: prompt,
	}
	if p.HumanType == "human+agent" {
		resp.Skill = strings.ReplaceAll(externalAgentSkill, "{name}", p.Name)
		resp.Skill = strings.ReplaceAll(resp.Skill, "{topic}", snap.Topic)
		resp.Skill = strings.ReplaceAll(resp.Skill, "{role}", p.Role)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("external: encode response failed: %v", err)
	}
}

// buildRoundGroups groups messages by round number.
func buildRoundGroups(messages []Message) []RoundGroup {
	groups := make(map[int]RoundGroup)
	for _, m := range messages {
		g := groups[m.Round]
		g.Round = m.Round
		g.Messages = append(g.Messages, m)
		groups[m.Round] = g
	}
	out := make([]RoundGroup, 0, len(groups))
	for round := 0; round < len(groups); round++ {
		if g, ok := groups[round]; ok {
			out = append(out, g)
		}
	}
	return out
}

// buildExternalPrompt returns the instruction for the external participant.
func buildExternalPrompt(snap RoomSnapshot, name string) string {
	switch snap.Status {
	case RoomWaiting:
		return "Room is starting, please wait and connect again shortly."
	case RoomStopped:
		return "Room has ended."
	}

	if snap.CurrentRound == 0 {
		return fmt.Sprintf("Your turn, %s. Write your first message.", name)
	}
	return fmt.Sprintf("Your turn, %s. Write your response (or \"OK\" if nothing to say).", name)
}
