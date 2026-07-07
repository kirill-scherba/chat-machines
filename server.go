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
- chat_stop: stop a room and collect summaries.
- chat_stats: get global statistics.

Rules:
- Each participant must have a unique name.
- Use max_rounds to prevent infinite chats.
- When a chat finishes, summaries are saved to memory-store-mcp automatically.
- You may participate in a chat as an adjunct: user messages go into the room, and you relay room messages back to the user.
`

// NewMCPServer creates and registers all chat-machines MCP tools.
func NewMCPServer(engine *ChatEngine) *server.MCPServer {
	s := server.NewMCPServer("chat-machines", "1.0.0",
		server.WithInstructions(systemInstructions))

	s.AddTools(
		chatCreateTool(engine),
		chatListTool(engine),
		chatGetTool(engine),
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
			mcp.Description("Maximum number of rounds (default: 10)")),
		mcp.WithBoolean("save_log",
			mcp.Description("Save full message log to SQLite (default: false)")),
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

			saveLog := false
			if v, ok := args["save_log"].(bool); ok {
				saveLog = v
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

			room, err := engine.CreateChatRoom(topic, configs, maxRounds, saveLog)
			if err != nil {
				return resultError(fmt.Errorf("create chat room: %w", err))
			}

			log.Printf("mcp chat_create: room=%s participants=%d", room.ID, len(configs))
			return resultJSON(map[string]any{
				"room_id":      room.ID,
				"topic":        room.Topic,
				"participants": configs,
				"max_rounds":   room.MaxRounds,
				"status":       room.Status,
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
			rooms := engine.ListRooms()
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
			room := engine.GetRoom(roomID)
			if room == nil {
				return resultError(fmt.Errorf("room %s not found", roomID))
			}
			return resultJSON(roomDetail(room))
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
			room := engine.GetRoom(roomID)
			return resultJSON(map[string]any{
				"room_id": roomID,
				"status":  room.Status,
				"rounds":  room.CurrentRound,
			})
		},
	}
}

func chatStatsTool(engine *ChatEngine) server.ServerTool {
	t := mcp.NewTool("chat_stats",
		mcp.WithDescription("Global chat statistics."))
	return server.ServerTool{
		Tool: t,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			rooms := engine.ListRooms()
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

func roomSummary(r *Room) map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.Participants))
	for i, p := range r.Participants {
		names[i] = p.Name
	}
	return map[string]any{
		"id":           r.ID,
		"topic":        r.Topic,
		"participants": names,
		"round":        r.CurrentRound,
		"max_rounds":   r.MaxRounds,
		"status":       r.Status,
	}
}

func roomDetail(r *Room) map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.Participants))
	for i, p := range r.Participants {
		names[i] = p.Name
	}
	var stoppedAt string
	if r.StoppedAt != nil {
		stoppedAt = r.StoppedAt.Format(time.RFC3339)
	}
	return map[string]any{
		"id":           r.ID,
		"topic":        r.Topic,
		"participants": names,
		"max_rounds":   r.MaxRounds,
		"current_round": r.CurrentRound,
		"status":       r.Status,
		"created_at":   r.CreatedAt.Format(time.RFC3339),
		"stopped_at":   stoppedAt,
		"messages":     r.Messages,
	}
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
