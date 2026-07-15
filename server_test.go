// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestChatAskToolResult verifies that the chat_ask MCP tool returns the
// expected JSON shape {agent, question, answer}.
func TestChatAskToolResult(t *testing.T) {
	responses := map[string]string{
		"ses_ask-svetik": "Great idea, go for it",
	}
	engine := NewChatEngine(newMockAgentClient(responses))
	defer engine.StopAll()

	srv := NewMCPServer(engine, "", nil)
	tool := srv.GetTool("chat_ask")
	if tool == nil {
		t.Fatal("chat_ask tool not registered")
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "chat_ask"
	req.Params.Arguments = map[string]any{
		"agent":    "svetik",
		"question": "оцени идею",
	}

	result, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("chat_ask handler error: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed["agent"] != "svetik" {
		t.Fatalf("expected agent svetik, got %v", parsed["agent"])
	}
	if parsed["question"] != "оцени идею" {
		t.Fatalf("expected question, got %v", parsed["question"])
	}
	if parsed["answer"] != "Great idea, go for it" {
		t.Fatalf("expected answer, got %v", parsed["answer"])
	}
	if _, ok := parsed["max_rounds"]; ok {
		t.Fatalf("unexpected max_rounds field in one-shot response")
	}
	if _, ok := parsed["transcript"]; ok {
		t.Fatalf("unexpected transcript field in one-shot response")
	}
}

// TestChatAskToolValidation verifies that missing required parameters produce
// an error response from the chat_ask MCP tool.
func TestChatAskToolValidation(t *testing.T) {
	engine := NewChatEngine(newMockAgentClient(map[string]string{}))
	defer engine.StopAll()

	srv := NewMCPServer(engine, "", nil)
	tool := srv.GetTool("chat_ask")
	if tool == nil {
		t.Fatal("chat_ask tool not registered")
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "chat_ask"
	req.Params.Arguments = map[string]any{
		"agent":    "svetik",
		"question": "",
	}

	result, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("chat_ask handler error: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed["error"] == "" {
		t.Fatalf("expected error field, got %s", text.Text)
	}
}

// TestAgentResourceReturnsAgentList verifies that the chat-machines://agents
// MCP resource returns the configured agent list.
func TestAgentResourceReturnsAgentList(t *testing.T) {
	engine := NewChatEngine(newMockAgentClient(map[string]string{}))
	defer engine.StopAll()

	cfg := &Config{
		Agents: []AgentInfo{
			{Name: "svetik", Model: "deepseek/deepseek-v4-flash"},
			{Name: "baron", Model: "deepseek/deepseek-v4-pro"},
		},
	}
	srv := NewMCPServer(engine, "", cfg)

	res := srv.ListResources()
	item, ok := res["chat-machines://agents"]
	if !ok {
		t.Fatalf("expected agents resource, got %v", res)
	}
	if item.Resource.URI != "chat-machines://agents" {
		t.Fatalf("expected agents resource URI, got %q", item.Resource.URI)
	}
}
