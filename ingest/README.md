# ingest

Receives event batches from agents and stores them in Postgres. The gRPC
server and the Redis stream publisher land in the next changes; today this
is the storage layer only.

## Storage

`internal/store` owns the `events` table and applies
`internal/store/schema.sql` on every start. A batch is stored whole or
rejected whole. The payload goes into the `data` column as protobuf JSON
with proto field names, so `data->>'user_agent'` works in SQL.

## Tests

The store tests need a real Postgres:

```sh
docker run -d --rm --name sentinel-test-pg -e POSTGRES_PASSWORD=test \
  -p 127.0.0.1:55432:5432 pgvector/pgvector:0.8.7-pg18
SENTINEL_TEST_POSTGRES_URL=postgres://postgres:test@127.0.0.1:55432/postgres \
  go test -race ./...
```

Without the variable they skip locally and fail in CI.
