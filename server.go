// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// MCP server and tool definitions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const systemInstructions = `You control the chat-machines server, which manages multi-AI chat rooms.

Available tools:
- chat_create: create a room with participants and a topic.
- chat_list: list active rooms.
- chat_get: get the full state of a room.
- chat_send: inject a message from a human into the room.
- chat_join: enter adjunct (human-in-chat) mode.
- chat_leave: exit adjunct (human-in-chat) mode.
- chat_stop: stop a room and collect summaries.
- chat_stats: get global statistics.

Rules:
- Each participant must have a unique name.
- Use max_rounds to prevent infinite chats.
- When a chat finishes, chat_stop returns a digest (summaries, topic, participants, rounds).
  The caller is responsible for saving it if needed.
- You may participate in a chat as an adjunct:
  1. chat_join(room_id) to connect your session to the room.
  2. poll chat_get(room_id) for new messages and relay them to the user.
  3. when the user speaks, call chat_send(room_id, message) to inject it.
  4. chat_leave(room_id) when done.
`

// NewMCPServer creates and registers all chat-machines MCP tools.
func NewMCPServer(engine *ChatEngine) *server.MCPServer {
	s := server.NewMCPServer("chat-machines", "1.0.0",
		server.WithInstructions(systemInstructions))

	s.AddTools(
		chatCreateTool(engine),
		chatListTool(engine),
		chatGetTool(engine),
		chatSendTool(engine),
		chatJoinTool(engine),
		chatLeaveTool(engine),
		chatStopTool(engine),
		chatStatsTool(engine),
	)
	return s
}

func resultJSON(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(b)), nil
}

func resultError(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(fmt.Sprintf(`{"error": %q}`, err.Error())), nil
}

func chatCreateTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_create",
		mcp.WithDescription("Create and start a multi-AI chat room."),
		mcp.WithArray("participants",
			mcp.Description("List of participant objects with name, optional agent, and optional model"),
			mcp.Required()),
		mcp.WithString("topic",
			mcp.Description("Topic of the chat"),
			mcp.Required()),
		mcp.WithNumber("max_rounds",
			mcp.Description("Maximum number of rounds (default: 10)"),
			mcp.DefaultNumber(10)),
	)
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			topic, _ := args["topic"].(string)
			if topic == "" {
				return resultError(fmt.Errorf("topic is required"))
			}

			maxRounds := 10
			if v, ok := args["max_rounds"].(float64); ok {
				maxRounds = int(v)
			}

			raw, ok := args["participants"].([]any)
			if !ok || len(raw) < 2 {
				return resultError(fmt.Errorf("at least 2 participants are required"))
			}

			configs := make([]ParticipantConfig, 0, len(raw))
			seen := make(map[string]bool)
			for _, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					return resultError(fmt.Errorf("participants must be objects"))
				}
				name, _ := m["name"].(string)
				if name == "" {
					return resultError(fmt.Errorf("participant name is required"))
				}
				if seen[name] {
					return resultError(fmt.Errorf("duplicate participant name %q", name))
				}
				seen[name] = true
				configs = append(configs, ParticipantConfig{
					Name:  name,
					Agent: getString(m, "agent"),
					Model: getString(m, "model"),
				})
			}

			roomID, err := engine.CreateChatRoom(topic, configs, maxRounds)
			if err != nil {
				return resultError(fmt.Errorf("create chat room: %w", err))
			}

			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok {
				return resultError(fmt.Errorf("room %s disappeared after creation", roomID))
			}

			log.Printf("mcp chat_create: room=%s participants=%d", snap.ID, len(configs))
			return resultJSON(map[string]any{
				"room_id":      snap.ID,
				"topic":        snap.Topic,
				"participants": configs,
				"max_rounds":   snap.MaxRounds,
				"status":       snap.Status,
			})
		},
	}
}

func chatListTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_list",
		mcp.WithDescription("List all chat rooms with their current state."))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			rooms := engine.ListRoomSnapshots()
			out := make([]map[string]any, 0, len(rooms))
			for _, r := range rooms {
				out = append(out, roomSummary(r))
			}
			return resultJSON(out)
		},
	}
}

func chatGetTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_get",
		mcp.WithDescription("Get the full state of a chat room."),
		mcp.WithString("room_id", mcp.Description("Room ID"), mcp.Required()))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			roomID, _ := req.GetArguments()["room_id"].(string)
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok {
				return resultError(fmt.Errorf("room %s not found", roomID))
			}
			return resultJSON(roomDetail(snap))
		},
	}
}

func chatSendTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_send",
		mcp.WithDescription("Send a message from a human into a chat room."),
		mcp.WithString("room_id", mcp.Description("Room ID"), mcp.Required()),
		mcp.WithString("message", mcp.Description("Message text"), mcp.Required()))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			roomID, _ := args["room_id"].(string)
			message, _ := args["message"].(string)
			if roomID == "" {
				return resultError(fmt.Errorf("room_id is required"))
			}
			if message == "" {
				return resultError(fmt.Errorf("message is required"))
			}
			const fromHuman = "Kirill"
			if err := engine.AddExternalMessage(roomID, fromHuman, message); err != nil {
				return resultError(err)
			}
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok {
				return resultError(fmt.Errorf("room %s not found after send", roomID))
			}
			return resultJSON(map[string]any{
				"room_id": roomID,
				"from":    fromHuman,
				"text":    message,
				"round":   snap.CurrentRound,
				"status":  snap.Status,
			})
		},
	}
}

func chatJoinTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_join",
		mcp.WithDescription("Join a chat room as a human adjunct."),
		mcp.WithString("room_id", mcp.Description("Room ID"), mcp.Required()),
		mcp.WithString("session_id",
			mcp.Description("Optional opencode-serve session ID for the adjunct")))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			roomID, _ := req.GetArguments()["room_id"].(string)
			if roomID == "" {
				return resultError(fmt.Errorf("room_id is required"))
			}
			sessionID, _ := req.GetArguments()["session_id"].(string)
			if err := engine.SetAdjunct(roomID, sessionID, "Kirill"); err != nil {
				return resultError(err)
			}
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok {
				return resultError(fmt.Errorf("room %s not found", roomID))
			}
			return resultJSON(map[string]any{
				"room_id":    roomID,
				"joined":     true,
				"topic":      snap.Topic,
				"adjunct":    "Kirill",
				"session_id": sessionID,
			})
		},
	}
}

func chatLeaveTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_leave",
		mcp.WithDescription("Leave a chat room as a human adjunct."),
		mcp.WithString("room_id", mcp.Description("Room ID"), mcp.Required()))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			roomID, _ := req.GetArguments()["room_id"].(string)
			if roomID == "" {
				return resultError(fmt.Errorf("room_id is required"))
			}
			if err := engine.ClearAdjunct(roomID); err != nil {
				return resultError(err)
			}
			return resultJSON(map[string]any{
				"room_id": roomID,
				"left":    true,
			})
		},
	}
}

func chatStopTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_stop",
		mcp.WithDescription("Stop a chat room and collect summaries."),
		mcp.WithString("room_id", mcp.Description("Room ID"), mcp.Required()))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			roomID, _ := req.GetArguments()["room_id"].(string)
			if err := engine.StopRoom(roomID); err != nil {
				return resultError(err)
			}
			snap, ok := engine.GetRoomSnapshot(roomID)
			if !ok {
				return resultJSON(map[string]any{
					"room_id": roomID,
					"status":  RoomStopped,
					"rounds":  0,
				})
			}
			result := map[string]any{
				"room_id": roomID,
				"status":  snap.Status,
				"rounds":  snap.CurrentRound,
			}
			if snap.StoredDigest != nil {
				result["digest"] = snap.StoredDigest
			}
			return resultJSON(result)
		},
	}
}

func chatStatsTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_stats",
		mcp.WithDescription("Global chat statistics."))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			rooms := engine.ListRoomSnapshots()
			totalRooms := len(rooms)
			active := 0
			stopped := 0
			totalMessages := 0
			var totalDuration time.Duration
			for _, r := range rooms {
				switch r.Status {
				case RoomActive:
					active++
				case RoomStopped:
					stopped++
				}
				totalMessages += len(r.Messages)
				if r.StoppedAt != nil {
					totalDuration += r.StoppedAt.Sub(r.CreatedAt)
				} else {
					totalDuration += time.Since(r.CreatedAt)
				}
			}
			return resultJSON(map[string]any{
				"total_rooms":    totalRooms,
				"active_rooms":   active,
				"stopped_rooms":  stopped,
				"total_messages": totalMessages,
				"total_duration": totalDuration.String(),
			})
		},
	}
}

func roomSummary(r RoomSnapshot) map[string]any {
	names := make([]string, len(r.Participants))
	for i, p := range r.Participants {
		names[i] = p.Name
	}
	m := map[string]any{
		"id":           r.ID,
		"topic":        r.Topic,
		"participants": names,
		"round":        r.CurrentRound,
		"max_rounds":   r.MaxRounds,
		"status":       r.Status,
	}
	if r.AdjunctName != "" {
		m["adjunct"] = r.AdjunctName
	}
	return m
}

func roomDetail(r RoomSnapshot) map[string]any {
	names := make([]string, len(r.Participants))
	for i, p := range r.Participants {
		names[i] = p.Name
	}
	var stoppedAt string
	if r.StoppedAt != nil {
		stoppedAt = r.StoppedAt.Format(time.RFC3339)
	}
	var adjunct any
	if r.AdjunctName != "" {
		adjunct = map[string]string{
			"name":       r.AdjunctName,
			"session_id": r.AdjunctSessionID,
		}
	}
	m := map[string]any{
		"id":            r.ID,
		"topic":         r.Topic,
		"participants":  names,
		"max_rounds":    r.MaxRounds,
		"current_round": r.CurrentRound,
		"status":        r.Status,
		"created_at":    r.CreatedAt.Format(time.RFC3339),
		"stopped_at":    stoppedAt,
		"messages":      r.Messages,
		"adjunct":       adjunct,
	}
	if r.StoredDigest != nil {
		m["digest"] = r.StoredDigest
	}
	return m
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
