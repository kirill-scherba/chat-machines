// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Summary persistence to memory-store-mcp.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Digest is the post-chat summary saved to memory-store-mcp.
type Digest struct {
	RoomID       string    `json:"room_id"`
	Topic        string    `json:"topic"`
	Participants []string  `json:"participants"`
	Rounds       int       `json:"rounds"`
	CreatedAt    time.Time `json:"created_at"`
	StoppedAt    time.Time `json:"stopped_at,omitempty"`
	Summaries    []Summary `json:"summaries"`
}

// MemorySummary saves chat digests to memory-store-mcp.
type MemorySummary struct {
	baseURL string
	client  *http.Client
}

// NewMemorySummary creates a saver for the given memory-store-mcp URL.
func NewMemorySummary(baseURL string) *MemorySummary {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:7708/mcp"
	}
	return &MemorySummary{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Save sends the digest to memory-store-mcp via the memory_save tool.
func (m *MemorySummary) Save(roomID string, digest Digest) error {
	value, err := json.Marshal(digest)
	if err != nil {
		return fmt.Errorf("marshal digest: %w", err)
	}

	key := "memory/project/chat-machines/digest/" + roomID

	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_save",
			"arguments": map[string]any{
				"key":   key,
				"value": string(value),
				"text":  buildDigestText(digest),
			},
		},
		"id": 1,
	}

	raw, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("memory_save http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("memory_save status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// buildDigestText creates a human-readable text representation for embedding.
func buildDigestText(d Digest) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Chat digest: topic=%q participants=%v rounds=%d\n", d.Topic, d.Participants, d.Rounds)
	for _, s := range d.Summaries {
		fmt.Fprintf(&b, "\nSummary from %s:\n%s\n", s.From, s.Text)
	}
	return b.String()
}
