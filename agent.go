// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Agent client: HTTP API to opencode-serve.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// AgentClient calls the opencode-serve HTTP API.
type AgentClient struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
}

// NewAgentClient creates a new client for the opencode-serve HTTP API.
func NewAgentClient(baseURL string, timeout time.Duration) *AgentClient {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:7709"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &AgentClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 90 * time.Second,
			},
		},
		timeout: timeout,
	}
}

// SessionInfo is the opencode-serve session metadata returned on creation.
type SessionInfo struct {
	ID        string `json:"id"`
	Slug      string `json:"slug,omitempty"`
	ProjectID string `json:"projectID,omitempty"`
	Directory string `json:"directory,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Version   string `json:"version,omitempty"`
	Title     string `json:"title,omitempty"`
}

// CreateSession calls POST /session and returns the session ID.
func (c *AgentClient) CreateSession(name, agent, model, workDir string) (string, error) {
	body := map[string]any{
		"name": name,
	}
	if agent != "" {
		body["agent"] = agent
	}
	if workDir != "" {
		body["directory"] = workDir
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal session body: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/session", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var sess SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return "", fmt.Errorf("decode session: %w", err)
	}
	if sess.ID == "" {
		return "", fmt.Errorf("create session returned empty id")
	}
	log.Printf("created opencode session %s for agent %q", sess.ID, agent)
	return sess.ID, nil
}

// messageResponse is the response from POST /session/{id}/message.
type messageResponse struct {
	Info struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"parts"`
}

// SendMessage calls POST /session/{id}/message and returns the assistant text response.
func (c *AgentClient) SendMessage(sessionID, text, agent, model string) (string, error) {
	body := c.buildMessageBody(text, agent, model)
	url := fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send message http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("send message status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var msg messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return "", fmt.Errorf("decode message: %w", err)
	}

	var texts []string
	for _, p := range msg.Parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n")), nil
}

// SendMessageAsync sends a message without waiting for the response.
func (c *AgentClient) SendMessageAsync(sessionID, text, agent, model string) error {
	body := c.buildMessageBody(text, agent, model)
	url := fmt.Sprintf("%s/session/%s/prompt_async", c.baseURL, sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("send async message http: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("send async message status %d", resp.StatusCode)
	}
	return nil
}

// GetLastResponse fetches the latest assistant message from a session.
func (c *AgentClient) GetLastResponse(sessionID string) (string, bool, error) {
	url := fmt.Sprintf("%s/session/%s/message?limit=1", c.baseURL, sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("new request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("get messages http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}

	var msgs []messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return "", false, fmt.Errorf("decode messages: %w", err)
	}

	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Info.ID == "" {
			continue
		}
		var texts []string
		for _, p := range msgs[i].Parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			return strings.TrimSpace(strings.Join(texts, "\n")), true, nil
		}
	}
	return "", false, nil
}

func (c *AgentClient) buildMessageBody(text, agent, model string) []byte {
	body := map[string]any{
		"parts": []map[string]string{
			{"type": "text", "text": text},
		},
		"noReply": false,
	}
	if agent != "" {
		body["agent"] = agent
	}
	if model != "" {
		providerID, modelID := splitModel(model)
		if providerID != "" && modelID != "" {
			body["model"] = map[string]string{
				"providerID": providerID,
				"modelID":    modelID,
			}
		}
	}
	raw, _ := json.Marshal(body)
	return raw
}

// splitModel splits "provider/model" into provider and model IDs.
func splitModel(s string) (providerID, modelID string) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}
