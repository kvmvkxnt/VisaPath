# proto

The schemas every Sentinel service shares: `Event` (one normalized log line)
and `IngestService` (how agents ship batches). Protobuf is also the payload
on Redis Streams (ADR 0005).

Every string in an `Event` comes from a log line, so it is
attacker-controlled. Treat it as data everywhere.

## Changing a schema

```sh
make proto-lint                              # buf lint + format check
make proto                                   # regenerate proto/gen
make proto-breaking BASE=origin/dev-fable    # wire-compatibility check
```

Generated code is committed; CI fails when it is stale. buf and the plugins
run through `go run` at pinned versions, so only Go needs to be installed.
New log sources are new cases in `Event.payload`, never changes to
existing fields.
