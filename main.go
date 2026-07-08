// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// chat-machines is a server for multi-AI chat rooms managed via MCP.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	ulog "github.com/kirill-scherba/log"
	"github.com/mark3labs/mcp-go/server"
)

const (
	appName    = "chat-machines"
	appVersion = "1.0.0"
)

func main() {
	// Configuration flags
	opencodeURL := flag.String("opencode-url", "http://127.0.0.1:7709", "opencode-serve HTTP API URL")
	httpAddr := flag.String("http", "", "MCP HTTP transport address (e.g. :8080); empty uses stdio")
	dbPath := flag.String("db", "", "SQLite database path for optional message logging")
	memoryMCP := flag.String("memory-mcp", "http://127.0.0.1:7708/mcp", "memory-store-mcp URL for saving summaries")
	autoSave := flag.Bool("auto-save", false, "automatically save digest to memory-store-mcp on chat stop")
	messageTimeout := flag.Duration("timeout", 120*time.Second, "per-message timeout")
	adjunctTimeout := flag.Duration("adjunct-timeout", 5*time.Minute, "human adjunct response timeout (default: 5m)")
	logDir := flag.String("log-dir", "", "log output directory (default: stderr only)")

	flag.Parse()

	// Initialize structured logging
	initLogging(*logDir)
	defer ulog.Close()

	log.Printf("%s %s starting (opencode=%s, http=%q)", appName, appVersion, *opencodeURL, *httpAddr)

	// Create agent client
	agentClient := NewAgentClient(*opencodeURL, *messageTimeout)

	// Create chat engine
	engine := NewChatEngine(agentClient)
	engine.SetAdjunctTimeout(*adjunctTimeout)

	// Optional persistence layer
	if *dbPath != "" {
		store, err := NewSQLiteStore(*dbPath)
		if err != nil {
			log.Fatalf("open sqlite store: %v", err)
		}
		defer store.Close()
		engine.SetLogger(store)
	}

	// Summary saver
	summary := NewMemorySummary(*memoryMCP)
	engine.SetSummarySaver(summary)
	engine.SetSummaryAutoSave(*autoSave)

	// Create MCP server
	mcpServer := NewMCPServer(engine)

	// Start transport
	if *httpAddr != "" {
		srv := server.NewStreamableHTTPServer(mcpServer, server.WithStateLess(true))
		go func() {
			log.Printf("MCP HTTP server listening on %s", *httpAddr)
			if err := srv.Start(*httpAddr); err != nil && err != http.ErrServerClosed {
				log.Printf("mcp http server error: %v", err)
			}
		}()

		// Wait for interrupt
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	} else {
		log.Println("MCP stdio server started")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("stdio server: %v", err)
		}
	}

	// Stop active chats gracefully
	engine.StopAll()
	log.Println("stopped")
}

func initLogging(logDir string) error {
	cfg := &ulog.Config{
		AppShort:  appName,
		AppType:   "DEV",
		UseStdout: false,
	}
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}
		cfg.FileConfig = &ulog.FileConfig{
			Folder:         logDir,
			CreateNewAfter: 24 * time.Hour,
			RemoveOldAfter: 30 * 24 * time.Hour,
		}
	}
	ulog.Init(cfg)
	return nil
}

func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, appName, "chat-machines.db")
}

// ChatLogger is an optional interface for message persistence.
type ChatLogger interface {
	LogMessage(roomID string, round int, from string, text string, createdAt time.Time) error
}
