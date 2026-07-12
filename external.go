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
	"time"
)

// ExternalEndpointResponse is the JSON body returned to external chat callers.
type ExternalEndpointResponse struct {
	Room      RoomSnapshot `json:"room"`
	Rounds    []RoundGroup `json:"rounds"`
	Prompt    string       `json:"prompt"`
	Skill     string       `json:"skill,omitempty"`
	NextSince int          `json:"next_since"`
}

// RoundGroup groups messages by round for the external endpoint response.
type RoundGroup struct {
	Round    int       `json:"round"`
	Messages []Message `json:"messages"`
}

// externalActionBody is decoded from the URL-encoded JSON body parameter.
type externalActionBody struct {
	Action   string `json:"action"`
	Message  string `json:"message,omitempty"`
	Since    int    `json:"since,omitempty"`
	From     string `json:"from,omitempty"`
	Messages []struct {
		From string `json:"from"`
		Text string `json:"text"`
	} `json:"messages,omitempty"`
}

// externalAgentSkill is returned on connect for human+agent participants.
const externalAgentSkill = `You are an AI relay and assistant for {name} in this chat room.

Room topic: {topic}
Your role: {role}

RELAY RULES:
- When Kirill says something, relay it verbatim as "Kirill: <message>" using send.
- When you (Коллега) want to say something yourself, use "Коллега: <message>".
- When other participants respond, display their messages — no commentary.
- Reply "OK" if nothing to say this round (silent relay).
- Never analyze others' messages unless Kirill asks.

TECHNICAL:
- The server sends you the current room state on every request.
- "next_since" in the response — pass it as "since" in your next "get".
- Use action "send" with the message text to post {name}'s response.`

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
		case "connect", "get":
			renderExternalResponse(w, engine, roomID, p, baseURL, body.Since)
		case "send":
			if body.Messages == nil {
				// Single message (backward compatible).
				if strings.TrimSpace(body.Message) == "" {
					http.Error(w, `{"error":"message is required for action=send"}`, http.StatusBadRequest)
					return
				}
				fromName := p.Name
				if body.From != "" {
					fromName = body.From
				}
				snapBefore, _ := engine.GetRoomSnapshot(roomID)
				since := len(snapBefore.Messages)
				if snapBefore.Mode == "roundless" {
					if _, err := engine.RoundlessSend(roomID, fromName, body.Message); err != nil {
						http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
						return
					}
				} else {
					roundBefore := snapBefore.CurrentRound
					if err := engine.store.AddExternalParticipantMessage(roomID, p.Name, body.Message); err != nil {
						http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusConflict)
						return
					}
					engine.WaitForRoundAdvance(roomID, roundBefore, p.Name, 30*time.Second)
				}
				renderExternalResponse(w, engine, roomID, p, baseURL, since)
			} else {
				// Batch messages: send multiple in one call.
				if len(body.Messages) == 0 {
					http.Error(w, `{"error":"messages array is empty"}`, http.StatusBadRequest)
					return
				}
				for _, m := range body.Messages {
					if strings.TrimSpace(m.Text) == "" {
						http.Error(w, `{"error":"each message must have text"}`, http.StatusBadRequest)
						return
					}
					if m.From == "" {
						m.From = p.Name
					}
				}
				snapBefore, _ := engine.GetRoomSnapshot(roomID)
				since := len(snapBefore.Messages)
				if snapBefore.Mode == "roundless" {
					// Roundless: add each message directly to transcripts, then trigger AI once.
					for _, m := range body.Messages {
						if err := engine.store.AddExternalParticipantMessage(roomID, p.Name, m.Text); err != nil {
							http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
							return
						}
						msg := Message{
							Round:     0,
							From:      m.From,
							Text:      m.Text,
							Timestamp: time.Now(),
						}
						if err := engine.store.AddMessage(roomID, msg); err != nil {
							http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
							return
						}
					}
					if _, err := engine.RoundlessSendAI(roomID); err != nil {
						http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
						return
					}
				} else {
					// Rounds: concatenate all messages into one (one message per round per external),
					// signal engine once, and wait for round advance.
					var parts []string
					for _, m := range body.Messages {
						parts = append(parts, m.From+": "+m.Text)
					}
					combined := strings.Join(parts, "\n")
					roundBefore := snapBefore.CurrentRound
					if err := engine.store.AddExternalParticipantMessage(roomID, p.Name, combined); err != nil {
						http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusConflict)
						return
					}
					engine.WaitForRoundAdvance(roomID, roundBefore, p.Name, 30*time.Second)
				}
				renderExternalResponse(w, engine, roomID, p, baseURL, since)
			}
		case "stop":
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok {
				http.Error(w, `{"error":"room not found"}`, http.StatusNotFound)
				return
			}
			if snap.Status == RoomStopped {
				http.Error(w, `{"error":"room already stopped"}`, http.StatusConflict)
				return
			}
			if err := engine.StopRoom(roomID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"stop room: %s"}`, err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status":  "stopped",
				"room_id": roomID,
			})
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
// If since > 0, only messages from that index onward are included in the response.
func renderExternalResponse(w http.ResponseWriter, engine *ChatEngine, roomID string, p Participant, baseURL string, since int) {
	snap, ok := engine.GetRoomSnapshot(roomID)
	if !ok {
		http.Error(w, `{"error":"room not found"}`, http.StatusNotFound)
		return
	}

	messages := snap.Messages
	totalMessages := len(messages)
	if since > 0 {
		if since < totalMessages {
			// Return only new messages to keep responses small.
			snap.Messages = messages[since:]
		} else {
			// since >= totalMessages: return empty.
			snap.Messages = nil
		}
	}

	groups := buildRoundGroups(snap.Messages)
	prompt := buildExternalPrompt(snap, p.Name)

	resp := ExternalEndpointResponse{
		Room:      snap,
		Rounds:    groups,
		Prompt:    prompt,
		NextSince: totalMessages,
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

// --- Room creation endpoint (POST /room/create) ---

// createRequestBody is the JSON body for POST /room/create.
type createRequestBody struct {
	Topic      string              `json:"topic"`
	MaxRounds  int                 `json:"max_rounds"`
	Mode       string              `json:"mode"`
	Participants []json.RawMessage `json:"participants"`
}

// CreateHandler returns an http.HandlerFunc for POST /room/create.
// Requires Bearer token matching apiKey.
func CreateHandler(engine *ChatEngine, baseURL, apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"only POST allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// Bearer auth check.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != apiKey {
			http.Error(w, `{"error":"invalid or missing API key"}`, http.StatusUnauthorized)
			return
		}

		var body createRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid JSON body: %s"}`, err), http.StatusBadRequest)
			return
		}

		if body.Topic == "" {
			http.Error(w, `{"error":"topic is required"}`, http.StatusBadRequest)
			return
		}
		if len(body.Participants) < 1 {
			http.Error(w, `{"error":"at least 1 participant required"}`, http.StatusBadRequest)
			return
		}
		if body.MaxRounds <= 0 {
			body.MaxRounds = 30
		}

		configs := make([]ParticipantConfig, 0, len(body.Participants))
		seen := make(map[string]bool)
		for _, raw := range body.Participants {
			var p struct {
				Name      string `json:"name"`
				Agent     string `json:"agent"`
				Model     string `json:"model"`
				Type      string `json:"type"`
				Role      string `json:"role"`
				HumanType string `json:"human_type"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid participant: %s"}`, err), http.StatusBadRequest)
				return
			}
			if p.Name == "" {
				http.Error(w, `{"error":"participant name is required"}`, http.StatusBadRequest)
				return
			}
			if seen[p.Name] {
				http.Error(w, fmt.Sprintf(`{"error":"duplicate participant name %q"}`, p.Name), http.StatusBadRequest)
				return
			}
			seen[p.Name] = true
			if p.Type == "" {
				p.Type = "ai"
			}
			if p.Type != "ai" && p.Type != "external" {
				http.Error(w, fmt.Sprintf(`{"error":"participant %q: type must be 'ai' or 'external'"}`, p.Name), http.StatusBadRequest)
				return
			}
			if p.Type == "external" && p.HumanType == "" {
				http.Error(w, fmt.Sprintf(`{"error":"participant %q: external requires human_type"}`, p.Name), http.StatusBadRequest)
				return
			}
			if p.Type == "external" && p.Role == "" {
				http.Error(w, fmt.Sprintf(`{"error":"participant %q: external requires role"}`, p.Name), http.StatusBadRequest)
				return
			}
			configs = append(configs, ParticipantConfig{
				Name:      p.Name,
				Agent:     p.Agent,
				Model:     p.Model,
				Type:      p.Type,
				Role:      p.Role,
				HumanType: p.HumanType,
			})
		}

		roomID, err := engine.CreateChatRoom(body.Topic, configs, body.MaxRounds, body.Mode)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"create room: %s"}`, err), http.StatusInternalServerError)
			return
		}

		snap, ok := engine.GetRoomSnapshot(roomID)
		if !ok {
			http.Error(w, `{"error":"room disappeared after creation"}`, http.StatusInternalServerError)
			return
		}

		// Build per-participant external URLs.
		externalURLs := make(map[string]string)
		if baseURL != "" {
			room := engine.GetRoom(roomID)
			if room != nil {
				room.mu.RLock()
				for _, p := range room.Participants {
					if p.IsExternal() {
						externalURLs[p.Name] = fmt.Sprintf("%s/room/%s/external?token=%s", baseURL, roomID, p.token)
					}
				}
				room.mu.RUnlock()
			}
		}

		log.Printf("ecc create: room=%s topic=%q participants=%d externals=%d", roomID, body.Topic, len(configs), len(externalURLs))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"room_id":       snap.ID,
			"topic":         snap.Topic,
			"participants":  configs,
			"max_rounds":    snap.MaxRounds,
			"mode":          snap.Mode,
			"status":        snap.Status,
			"external_urls": externalURLs,
		})
	}
}

// --- OpenAPI schema endpoint ---

// OpenAPIHandler serves the OpenAPI 3.0 spec for ChatGPT Actions integration.
func OpenAPIHandler(engine *ChatEngine, baseURL, apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		spec := map[string]any{
			"openapi": "3.1.0",
			"info": map[string]any{
				"title":       "Chat Machines API",
				"description": "Multi-AI chat room management for ChatGPT Actions",
				"version":     "1.0.0",
			},
			"servers": []map[string]any{
				{"url": baseURL},
			},
		}

		// Security scheme (only if apiKey is set).
		if apiKey != "" {
			spec["components"] = map[string]any{
				"schemas": map[string]any{},
				"securitySchemes": map[string]any{
					"ApiKeyAuth": map[string]any{
						"type":         "http",
						"scheme":       "bearer",
						"description": "API key for room creation",
					},
				},
			}
		}

		// Paths.
		type schemaObj map[string]any
		type paramObj map[string]any

		paths := map[string]any{
			"/room/create": map[string]any{
				"post": map[string]any{
					"summary":     "Create a new chat room",
					"description": "Creates a chat room with AI and external participants. External participants receive URLs to join via HTTP.",
					"operationId": "createRoom",
					"security": []map[string]any{
						{"ApiKeyAuth": []string{}},
					},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": schemaObj{
									"type": "object",
									"properties": schemaObj{
										"topic": schemaObj{
											"type":        "string",
											"description": "Chat topic",
											"example":     "Chess club meeting",
										},
										"max_rounds": schemaObj{
											"type":        "integer",
											"description": "Max chat rounds (default 30)",
											"default":     30,
										},
										"mode": schemaObj{
											"type":        "string",
											"enum":        []string{"rounds", "roundless"},
											"description": "Chat mode: 'rounds' (default, round-based) or 'roundless' (send triggers AI directly)",
										},
										"participants": schemaObj{
											"type": "array",
											"items": schemaObj{
												"type": "object",
												"properties": schemaObj{
													"name": schemaObj{
														"type":        "string",
														"description": "Participant name",
														"example":     "Baron",
													},
													"agent": schemaObj{
														"type":        "string",
														"description": "AI agent name (for type=ai)",
														"example":     "baron",
													},
													"type": schemaObj{
														"type":        "string",
														"enum":        []string{"ai", "external"},
														"description": "'ai' (default) or 'external'",
													},
													"role": schemaObj{
														"type":        "string",
														"enum":        []string{"controller", "participant"},
														"description": "Required for external participants",
													},
													"human_type": schemaObj{
														"type":        "string",
														"enum":        []string{"human", "human+agent"},
														"description": "Required for external participants",
													},
												},
												"required": []string{"name"},
											},
										},
									},
									"required": []string{"topic", "participants"},
								},
							},
						},
					},
					"responses": schemaObj{
						"200": schemaObj{
							"description": "Room created successfully",
							"content": schemaObj{
								"application/json": schemaObj{
									"schema": schemaObj{
										"type": "object",
										"properties": schemaObj{
											"room_id":   schemaObj{"type": "string"},
											"topic":     schemaObj{"type": "string"},
											"status":    schemaObj{"type": "string"},
											"max_rounds": schemaObj{"type": "integer"},
											"mode":       schemaObj{"type": "string"},
											"participants": schemaObj{"type": "array"},
											"external_urls": schemaObj{
												"type":        "object",
												"description": "Map of participant name → ECC URL with token",
											},
										},
									},
								},
							},
						},
						"401": schemaObj{
							"description": "Invalid or missing API key",
						},
					},
				},
			},
			"/room/{room_id}/external": map[string]any{
				"get": map[string]any{
					"summary":     "Interact with a chat room",
					"description": "Connect, send messages, or get room state via query parameters",
					"operationId": "roomAction",
					"parameters": []paramObj{
						{
							"name":     "room_id",
							"in":       "path",
							"required": true,
							"schema":   schemaObj{"type": "string"},
						},
						{
							"name":     "token",
							"in":       "query",
							"required": true,
							"schema":   schemaObj{"type": "string"},
						},
						{
							"name":     "body",
							"in":       "query",
							"required": true,
							"schema": schemaObj{
								"type": "string",
								"description": "URL-encoded JSON: {\"action\":\"connect|get|send|stop\", \"message\":\"...\", \"since\":0, \"from\":\"...\", \"messages\":[{\"from\":\"Name\",\"text\":\"...\"}]} — for action=send: message (single) or messages (batch, roundless); from sets sender name for single sends",
							},
						},
					},
					"responses": schemaObj{
						"200": schemaObj{
							"description": "Room state / action result",
							"content": schemaObj{
								"application/json": schemaObj{
									"schema": schemaObj{
										"type": "object",
										"properties": schemaObj{
											"room":       schemaObj{"type": "object"},
											"rounds":     schemaObj{"type": "array"},
											"prompt":     schemaObj{"type": "string"},
											"next_since": schemaObj{"type": "integer"},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		spec["paths"] = paths

		json.NewEncoder(w).Encode(spec)
	}
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
