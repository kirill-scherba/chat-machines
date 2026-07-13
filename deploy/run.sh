#!/bin/bash
# Chat Machines run wrapper — sources ~/.config/chat-machines/env if present
if [ -f "${HOME}/.config/chat-machines/env" ]; then
    set -a
    source "${HOME}/.config/chat-machines/env"
    set +a
fi

exec "${HOME}/.local/bin/chat-machines" \
    --http "${CM_HTTP:-:8083}" \
    --external-addr "${CM_EXTERNAL_ADDR:-:8084}" \
    --external-base-url "${CM_EXTERNAL_BASE_URL}" \
    --api-key "${CM_API_KEY}" \
    --opencode-url "${CM_OPENCODE_URL:-http://127.0.0.1:7709}" \
    --timeout "${CM_TIMEOUT:-2m}" \
    --adjunct-timeout "${CM_ADJUNCT_TIMEOUT:-5m}" \
    --log-dir "${CM_LOG_DIR}" \
    --auto-save
