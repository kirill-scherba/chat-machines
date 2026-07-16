// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Config{
		Agents: []AgentInfo{
			{Name: "test-agent", Model: "provider/model", Specialty: "testing"},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(loaded.Agents))
	}
	if loaded.Agents[0].Name != "test-agent" {
		t.Fatalf("expected test-agent, got %q", loaded.Agents[0].Name)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestConfigGetAgent(t *testing.T) {
	cfg := Config{
		Agents: []AgentInfo{
			{Name: "Svetik", Model: "deepseek/deepseek-v4-flash"},
			{Name: "Baron", Model: "deepseek/deepseek-v4-pro"},
		},
	}
	if got := cfg.GetAgent("svetik"); got == nil || got.Name != "Svetik" {
		t.Fatalf("expected agent Svetik, got %v", got)
	}
	if got := cfg.GetAgent("BARON"); got == nil || got.Name != "Baron" {
		t.Fatalf("expected agent Baron, got %v", got)
	}
	if got := cfg.GetAgent("unknown"); got != nil {
		t.Fatalf("expected nil for unknown agent, got %v", got)
	}
}

func TestConfigAgentNames(t *testing.T) {
	cfg := Config{Agents: []AgentInfo{{Name: "a"}, {Name: "b"}}}
	names := cfg.AgentNames()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("expected [a b], got %v", names)
	}
}

func TestLoadOrDefaultMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	// Isolate CWD: FindConfig() includes "./config.json" as fallback.
	// The repo root has a real config.json — must chdir to temp dir.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	cfg := LoadOrDefault(nil)
	if cfg != nil {
		t.Fatalf("expected nil when file missing, got %v", cfg)
	}
}

func TestLoadOrDefaultValidFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgDir := filepath.Join(dir, ".config", appName)
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(cfgDir, "config.json")
	if err := os.WriteFile(path, []byte(`{"agents":[{"name":"custom"}]}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded := LoadOrDefault(nil)
	if len(loaded.Agents) != 1 || loaded.Agents[0].Name != "custom" {
		t.Fatalf("expected custom agent from file, got %v", loaded.Agents)
	}
}
