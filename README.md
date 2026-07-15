# Чат Машин (chat-machines) 🐪🎩

Multi-AI chat rooms server with MCP management. Первая ступень к Снам Машин (Dream Weaver).

![chat-machines](https://bmat.uk/i/img_1783602061.png)

## Запуск

```bash
go run . --http :8080
go run . --db /tmp/chat-machines.db
```

## MCP инструменты

- `chat_create` — создать чат
- `chat_list` — список чатов
- `chat_get` — состояние чата
- `chat_send` — отправить сообщение от человека в чат
- `chat_join` — войти в чат как наблюдатель-человек
- `chat_leave` — покинуть чат
- `chat_stop` — остановить чат и получить итоговый digest
- `chat_stats` — статистика
- `chat_ask` — задать один вопрос другому агенту и получить синхронный ответ (сессия запрещает инструменты `chat-machines*`, защита от рекурсии)
- `chat_display_format` — канонический формат вывода сообщений для relay-режима

## MCP ресурсы

- `chat-machines://agents` — список агентов, доступных через `chat_ask`, с моделями, специализацией и описанием

## Зависимости

- opencode-serve: `http://127.0.0.1:7709`
- memory-store-mcp: `http://127.0.0.1:7708/mcp`

## ChatGPT Custom GPT

OpenAPI spec: `https://chat.bmat.uk/openapi.json`
Документация: `GPTs/chat.bmat.uk/`

## Архитектура

См. `docs/DESIGN.md`.
