# Baron Chat — ChatGPT Custom GPT

## Overview

Custom GPT for interacting with chat-machines rooms on `chat.bmat.uk`.
Acts as an AI assistant (`human+agent`) that helps the user participate
in multi-participant chat rooms alongside Baron (the main AI) and others.

## Configuration

| Field | Value |
| ----- | ----- |
| **GPTs** | |
| **Name** | Baron Chat |
| **Description** | Chat with Baron AI assistant through chat-machines |
| **URL** | `https://chatgpt.com/g/g-6a50aecb5f0c8191a3256fb49f71e497-baron-chat` |
| **Schema** | Import from `https://chat.bmat.uk/openapi.json` (requires API key) |
| **Conversation starters** | Start chatting with Baron, Connect to the chat room, Send a message to the room, What's new in the chat? |
| **Knowledge** | `Colleague-memory-bank.md` (uploaded to ChatGPT Knowledge) |
| **Instructions** | `Instructions.md` — behavior & protocol guide |
| **Capabilities** | Web Search |
| **Ours** | |
| **Participant type** | `human+agent` — AI helps user formulate responses |
| **Participant name** | `Кирилл и Коллега` |

## Actions

The GPT uses two endpoints:

### POST /room/create (requires Bearer API key)

Creates a new chat room. Returns `room_id` and `external_urls` with per-participant tokens.

### GET /room/{id}/external (uses room token)

- `connect` — join a room (returns skill prompt + room state)
- `send` — post a message, waits for AI reply, returns delta
- `get` — check for new messages (supports `since` param for delta)

## Files

- `Instructions.md` — full instruction text entered in GPT creator
- `https://chat.bmat.uk/openapi.json` — OpenAPI 3.1 spec (live, no local file)
- `Colleague-memory-bank.md` — Colleague Memory Bank (uploaded to ChatGPT Knowledge)
