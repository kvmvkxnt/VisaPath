# ingest

Receives event batches from agents over gRPC with mTLS, stores them in
Postgres and publishes them to the Redis stream `events`, one entry per
event with the protobuf bytes in the field `pb` (ADR 0005).

## Guarantees

- Only clients with a certificate signed by the configured CA can connect
  (TLS 1.3). The event `host` is always the certificate's common name,
  whatever the agent sent.
- A batch is stored and published whole or not at all. The Postgres
  transaction commits only after Redis accepted the batch, so resends while
  Redis is down leave no duplicate rows.
- `InvalidArgument` means the batch can never be accepted (bad IP, missing
  time or payload, more than 1000 events); the agent drops it. Any other
  error is safe to retry.
- Stream consumers see at-least-once delivery: a commit that fails after a
  successful publish leads to a second publish on resend.

## Storage

`internal/store` owns the `events` table and applies
`internal/store/schema.sql` on every start. The payload goes into the
`data` column as protobuf JSON with proto field names, so
`data->>'user_agent'` works in SQL. NUL bytes in log strings are stored as
U+FFFD because Postgres refuses them.

## Configuration

All required, read from the environment at start:

| Key                    | Meaning |
|------------------------|---------|
| `POSTGRES_URL`         | `postgres://user:pass@host:5432/db` |
| `REDIS_URL`            | `redis://host:6379` |
| `INGEST_LISTEN`        | gRPC listen address, e.g. `:8443` |
| `INGEST_TLS_CERT`      | Server certificate, PEM file |
| `INGEST_TLS_KEY`       | Server private key, PEM file |
| `INGEST_TLS_CLIENT_CA` | CA that signs agent certificates, PEM file |

## Run and test

```sh
go run ./cmd/sentinel-ingest
```

The tests need a real Postgres and Redis:

```sh
docker run -d --rm --name sentinel-test-pg -e POSTGRES_PASSWORD=test \
  -p 127.0.0.1:55432:5432 pgvector/pgvector:0.8.7-pg18
docker run -d --rm --name sentinel-test-redis \
  -p 127.0.0.1:56379:6379 redis:8.10.3-alpine
SENTINEL_TEST_POSTGRES_URL=postgres://postgres:test@127.0.0.1:55432/postgres \
SENTINEL_TEST_REDIS_URL=redis://127.0.0.1:56379 \
  go test -race ./...
```

Without the variables they skip locally and fail in CI.
