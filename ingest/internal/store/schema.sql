-- Applied on every start, so every statement must be idempotent.

-- CREATE ... IF NOT EXISTS still races when two processes start against an
-- empty database. The whole file runs as one transaction; this serializes it.
SELECT pg_advisory_xact_lock(736410001);

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

-- Docker events have no remote address.
ALTER TABLE events ALTER COLUMN src_ip DROP NOT NULL;
