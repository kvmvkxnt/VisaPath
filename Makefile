BUF := go run github.com/bufbuild/buf/cmd/buf@v1.75.0

.PHONY: proto proto-lint proto-breaking agent

# Regenerate code from proto/. Generated code is committed.
proto:
	cd proto && $(BUF) generate

proto-lint:
	cd proto && $(BUF) lint && $(BUF) format -d --exit-code

# Usage: make proto-breaking BASE=origin/dev-fable
proto-breaking:
	cd proto && $(BUF) breaking --against '../.git#ref=$(BASE),subdir=proto'

# Static agent binary for a monitored host. Override GOARCH=arm64 as needed.
GOARCH ?= amd64
agent:
	cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags=-s \
		-o ../dist/sentinel-agent-linux-$(GOARCH) ./cmd/sentinel-agent
