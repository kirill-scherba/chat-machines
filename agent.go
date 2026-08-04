// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Agent client: delegates to opencode-client for opencode-serve HTTP API calls.
package main

import (
	"log"
	"time"

	opencodeclient "github.com/kirill-scherba/opencode-client"
)

// AgentClient implements the Agent interface using opencode-client.
type AgentClient struct {
	client *opencodeclient.Client
}

// NewAgentClient creates a new AgentClient backed by opencode-client.
func NewAgentClient(baseURL string, timeout time.Duration) *AgentClient {
	return &AgentClient{
		client: opencodeclient.New(baseURL, timeout),
	}
}

// CreateSession calls POST /session and returns the session ID.
func (c *AgentClient) CreateSession(name, agent, model, workDir, source string, permissions []PermissionRule) (string, error) {
	// Convert local PermissionRule to opencodeclient.PermissionRule.
	ocPerms := make([]opencodeclient.PermissionRule, len(permissions))
	for i, p := range permissions {
		ocPerms[i] = opencodeclient.PermissionRule{
			Permission: "tools",
			Pattern:    p.Prefix,
			Action:     denyAction(p.Deny),
		}
	}
	sess, err := c.client.CreateSession(name, agent, model, workDir, source, ocPerms)
	if err != nil {
		return "", err
	}
	log.Printf("created opencode session %s for agent %q", sess.ID, agent)
	return sess.ID, nil
}

// denyAction converts the Deny bool to the opencode-serve permission action string.
func denyAction(deny bool) string {
	if deny {
		return "deny"
	}
	return "allow"
}

// SendMessage calls POST /session/{id}/message and returns the assistant text response.
func (c *AgentClient) SendMessage(sessionID, text, agent, model string) (string, error) {
	// Build a temporary Session with the caller-provided values.
	// (chat-machines passes agent and model explicitly on every call.)
	return c.client.SendMessage(&opencodeclient.Session{ID: sessionID, Agent: agent, Model: model}, text)
}

// SendMessageAsync sends a message without waiting for the response.
func (c *AgentClient) SendMessageAsync(sessionID, text, agent, model string) error {
	return c.client.SendMessageAsync(&opencodeclient.Session{ID: sessionID, Agent: agent, Model: model}, text)
}

// CloseSession sends DELETE /session/{id} to opencode-serve, releasing the session.
func (c *AgentClient) CloseSession(sessionID string) error {
	err := c.client.CloseSessionID(sessionID)
	if err != nil {
		return err
	}
	log.Printf("closed session %s", sessionID)
	return nil
}

// GetLastResponse fetches the latest assistant message from a session.
func (c *AgentClient) GetLastResponse(sessionID string) (string, bool, error) {
	return c.client.GetLastResponse(sessionID)
}
