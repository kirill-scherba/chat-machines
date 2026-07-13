#!/bin/bash
# Install Chat Machines as a systemd user service
set -e

BINARY_SRC="${1:-./chat-machines}"
SERVICE_NAME="chat-machines"
CONFIG_DIR="$HOME/.config/chat-machines"
SERVICE_DIR="$HOME/.config/systemd/user"
BIN_DIR="$HOME/.local/bin"

echo "=== Installing Chat Machines ==="

mkdir -p "$CONFIG_DIR" "$SERVICE_DIR" "$BIN_DIR"

# Install binary
if [ -f "$BINARY_SRC" ]; then
    cp "$BINARY_SRC" "$BIN_DIR/chat-machines"
    chmod +x "$BIN_DIR/chat-machines"
    echo "Binary installed to $BIN_DIR/chat-machines"
fi

# Install run wrapper
cp ./deploy/run.sh "$BIN_DIR/chat-machines-run.sh"
chmod +x "$BIN_DIR/chat-machines-run.sh"
echo "Wrapper installed to $BIN_DIR/chat-machines-run.sh"

# Install env file (don't overwrite existing)
if [ ! -f "$CONFIG_DIR/env" ]; then
    cp ./deploy/env.example "$CONFIG_DIR/env"
    echo "Environment file created at $CONFIG_DIR/env"
    echo "  -> Edit it and set CM_API_KEY"
fi

# Install and enable service
cp ./deploy/chat-machines.service "$SERVICE_DIR/$SERVICE_NAME.service"

systemctl --user daemon-reload
systemctl --user enable "$SERVICE_NAME"

echo ""
echo "=== Starting service ==="
systemctl --user restart "$SERVICE_NAME" || true
sleep 1
systemctl --user status "$SERVICE_NAME" --no-pager

echo ""
echo "=== Done ==="
echo "  Start:  systemctl --user start $SERVICE_NAME"
echo "  Stop:   systemctl --user stop $SERVICE_NAME"
echo "  Logs:   journalctl --user -u $SERVICE_NAME -f"
echo "  Config: $CONFIG_DIR/env"
echo ""
echo "  Edit $CONFIG_DIR/env, then: systemctl --user restart $SERVICE_NAME"
