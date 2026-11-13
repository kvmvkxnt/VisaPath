-- Applied on every start, so every statement must be idempotent.
CREATE TABLE IF NOT EXISTS events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    time        timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    host        text NOT NULL,
    src_ip      inet NOT NULL,
    kind        text NOT NULL,
    -- The payload message as protobuf JSON with proto field names.
    data        jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS events_src_ip_time_idx ON events (src_ip, time DESC);
CREATE INDEX IF NOT EXISTS events_time_idx ON events (time DESC);
