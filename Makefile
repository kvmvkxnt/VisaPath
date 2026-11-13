BUF := go run github.com/bufbuild/buf/cmd/buf@v1.75.0

.PHONY: proto proto-lint proto-breaking

# Regenerate code from proto/. Generated code is committed.
proto:
	cd proto && $(BUF) generate

proto-lint:
	cd proto && $(BUF) lint && $(BUF) format -d --exit-code

# Usage: make proto-breaking BASE=origin/dev-fable
proto-breaking:
	cd proto && $(BUF) breaking --against '../.git#ref=$(BASE),subdir=proto'
