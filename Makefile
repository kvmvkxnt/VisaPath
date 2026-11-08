BUF := go run github.com/bufbuild/buf/cmd/buf@v1.75.0

.PHONY: proto
# Format, lint and regenerate code from proto/. Commit the result.
proto:
	cd proto && $(BUF) format -w && $(BUF) lint && $(BUF) generate
	cd proto/gen/go && go mod tidy
