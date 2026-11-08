# proto

Shared schemas for everything that crosses a service boundary: events from
agents, the ingest RPC, and the messages on Redis Streams (ADR 0005).

- `sentinel/v1/*.proto` is the source of truth.
- `gen/go` is generated Go code, committed, importable as
  `github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1`.

## Changing a schema

Edit the `.proto` files, then from the repo root:

```
make proto
```

That formats, lints and regenerates. Commit the `.proto` change and the
generated code together. CI fails if they are out of sync, if lint fails, or
if the change breaks wire compatibility with the base branch (`buf breaking`).

Adding fields and new `oneof` cases is fine. Renaming, renumbering or
removing a field is a breaking change: reserve the number instead.
