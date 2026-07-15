// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Agent registry configuration loader.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentInfo describes a known agent available via chat_ask.
type AgentInfo struct {
	// Name is the opencode-serve agent identifier, e.g. "svetik".
	Name string `json:"name"`
	// Model is the default model for the agent, formatted as provider/model.
	Model string `json:"model,omitempty"`
	// Specialty describes what the agent does best.
	Specialty string `json:"specialty,omitempty"`
	// When describes situations where this agent should be used.
	When string `json:"when,omitempty"`
	// Description is a full human-readable description of the agent.
	Description string `json:"description,omitempty"`
}

// Config is the chat-machines configuration loaded from
// ~/.config/chat-machines/config.json.
type Config struct {
	Agents []AgentInfo `json:"agents"`
}

// FindConfig locates the chat-machines config file in standard locations.
// It returns the path and a boolean indicating whether the file exists.
func FindConfig() (string, bool) {
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), ".config", "chat-machines", "config.json"),
	}
	if cfgDir, err := os.UserConfigDir(); err == nil {
		candidates = append([]string{filepath.Join(cfgDir, appName, "config.json")}, candidates...)
	}
	candidates = append(candidates, "./config.json")

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// LoadConfig reads and parses the chat-machines configuration from path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// DefaultConfig returns the built-in agent registry used when no config file is
// present or when the file cannot be loaded.
func DefaultConfig() *Config {
	return &Config{
		Agents: []AgentInfo{
			{
				Name:        "baron",
				Model:       "deepseek/deepseek-v4-flash",
				Specialty:   "универсальный ассистент, управляющий экосистемой",
				When:        "общие вопросы, координация, поиск информации",
				Description: "Baron (Барон) — главный AI-ассистент экосистемы Matrica.",
			},
			{
				Name:        "architect",
				Model:       "deepseek/deepseek-v4-pro",
				Specialty:   "архитектурное проектирование",
				When:        "проектирование систем, выбор технологий",
				Description: "Architect (Архитектор) — проектирует системы и выбирает технологии.",
			},
			{
				Name:        "master",
				Model:       "ollama-cloud/kimi-k2.7-code",
				Specialty:   "мастер-разработчик, чистый код",
				When:        "написание и рефакторинг кода, тестирование",
				Description: "Master (Мастер) — разработчик, отвечающий за качество кода.",
			},
			{
				Name:        "guard",
				Model:       "openai/gpt-5.5",
				Specialty:   "безопасность, проверка рисков",
				When:        "аудит безопасности, проверка уязвимостей",
				Description: "Guard (Страж) — проверяет безопасность и риски.",
			},
			{
				Name:        "manager",
				Model:       "deepseek/deepseek-v4-flash",
				Specialty:   "менеджер задач и приоритетов",
				When:        "планирование, приоритизация, статус",
				Description: "Manager (Менеджер) — следит за задачами и сроками.",
			},
			{
				Name:        "svetik",
				Model:       "deepseek/deepseek-v4-flash",
				Specialty:   "архитектурный наблюдатель, мера идей",
				When:        "оценка идей, поиск слабых мест, ревью концепций",
				Description: "Svetik (Светик) — pure observation agent, оценивает идеи и находит слабые места.",
			},
			{
				Name:        "falconer",
				Model:       "deepseek/deepseek-v4-pro",
				Specialty:   "поиск и исследование",
				When:        "поиск информации, исследования",
				Description: "Falconer (Сокольничий) — ищет и исследует информацию.",
			},
			{
				Name:        "stargazer",
				Model:       "openai/gpt-5.5",
				Specialty:   "стратегия и видение",
				When:        "стратегическое планирование, видение продукта",
				Description: "Stargazer (Звездочёт) — думает о стратегии и будущем.",
			},
			{
				Name:        "merchant",
				Model:       "openai/gpt-5.5",
				Specialty:   "бизнес, инвесторы, продажи",
				When:        "бизнес-модель, переговоры, рост",
				Description: "Merchant (Купец) — отвечает за бизнес, инвесторов и рост.",
			},
		},
	}
}

// LoadOrDefault loads the config from the first available config file;
// if no file is found or loading fails, it logs a warning and returns DefaultConfig.
func LoadOrDefault(logger func(string, ...any)) *Config {
	if path, ok := FindConfig(); ok {
		cfg, err := LoadConfig(path)
		if err != nil {
			if logger != nil {
				logger("config load failed, using defaults: %v", err)
			}
			return DefaultConfig()
		}
		if logger != nil {
			logger("loaded config from %s (%d agents)", path, len(cfg.Agents))
		}
		return cfg
	}
	if logger != nil {
		logger("config not found, using default agents")
	}
	return DefaultConfig()
}

// GetAgent looks up an agent by name (case-insensitive).
func (c *Config) GetAgent(name string) *AgentInfo {
	name = strings.ToLower(name)
	for i := range c.Agents {
		if strings.ToLower(c.Agents[i].Name) == name {
			return &c.Agents[i]
		}
	}
	return nil
}

// AgentNames returns the names of all registered agents.
func (c *Config) AgentNames() []string {
	names := make([]string, len(c.Agents))
	for i, a := range c.Agents {
		names[i] = a.Name
	}
	return names
}

// AgentSummaries returns short summaries of all agents for the MCP resource.
func (c *Config) AgentSummaries() []map[string]string {
	out := make([]map[string]string, 0, len(c.Agents))
	for _, a := range c.Agents {
		out = append(out, map[string]string{
			"name":        a.Name,
			"model":       a.Model,
			"specialty":   a.Specialty,
			"when":        a.When,
			"description": a.Description,
		})
	}
	return out
}
