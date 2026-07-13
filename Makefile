.PHONY: build build-linux clean install deploy

BINARY=chat-machines
SERVICE_NAME=chat-machines

build:
	go build -o $(BINARY) .

build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux .

clean:
	rm -f $(BINARY) $(BINARY)-linux

# Local install via systemd --user
install: build
	./deploy/install.sh ./$(BINARY)

# Deploy to Hetzner: make deploy HOST=user@server
HOST ?= kirill@135.181.136.120

deploy: build-linux
	@echo "=== Deploying to $(HOST) ==="
	scp $(BINARY)-linux $(HOST):/tmp/$(BINARY)
	scp deploy/run.sh $(HOST):/tmp/$(SERVICE_NAME)-run.sh
	scp deploy/chat-machines.service $(HOST):/tmp/
	scp deploy/env.example $(HOST):/tmp/$(SERVICE_NAME).env.example
	ssh $(HOST) ' \
		BIN_DIR=$$HOME/.local/bin && \
		CONFIG_DIR=$$HOME/.config/$(SERVICE_NAME) && \
		SERVICE_DIR=$$HOME/.config/systemd/user && \
		mkdir -p $$BIN_DIR $$CONFIG_DIR $$SERVICE_DIR && \
		cp /tmp/$(BINARY) $$BIN_DIR/$(BINARY) && chmod +x $$BIN_DIR/$(BINARY) && \
		cp /tmp/$(SERVICE_NAME)-run.sh $$BIN_DIR/$(SERVICE_NAME)-run.sh && chmod +x $$BIN_DIR/$(SERVICE_NAME)-run.sh && \
		cp /tmp/chat-machines.service $$SERVICE_DIR/ && \
		[ -f $$CONFIG_DIR/env ] || cp /tmp/$(SERVICE_NAME).env.example $$CONFIG_DIR/env && \
		systemctl --user daemon-reload && \
		systemctl --user enable $(SERVICE_NAME) && \
		systemctl --user restart $(SERVICE_NAME) && \
		echo "Service deployed" && \
		sleep 1 && systemctl --user status $(SERVICE_NAME) --no-pager \
	'
	@echo ""
	@echo "=== Then on server ==="
	@echo "  vi ~/.config/chat-machines/env  # set CM_API_KEY"
	@echo "  systemctl --user restart chat-machines"
