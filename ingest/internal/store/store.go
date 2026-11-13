// Package store writes events to Postgres.
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// NOTE: one idempotent file is enough while changes are additive. The first
// change that is not (rename, drop, backfill) needs a real migration tool.
//
//go:embed schema.sql
var schema string

// ErrInvalidEvent marks a batch that can never be stored, so resending it
// is pointless.
var ErrInvalidEvent = errors.New("invalid event")

var payloadJSON = protojson.MarshalOptions{UseProtoNames: true}

// Store is the Postgres side of ingest.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres and applies the schema.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Insert stores the batch whole or not at all. publish runs after the rows
// are written and before the commit; if it fails nothing is stored, so a
// resent batch does not pile up duplicates while the stream is down.
func (s *Store) Insert(ctx context.Context, events []*sentinelv1.Event, publish func(context.Context) error) error {
	rows := make([][]any, len(events))
	for i, e := range events {
		row, err := toRow(e)
		if err != nil {
			return fmt.Errorf("%w: event %d: %v", ErrInvalidEvent, i, err)
		}
		rows[i] = row
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"events"},
		[]string{"time", "host", "src_ip", "kind", "data"},
		pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if err := publish(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func toRow(e *sentinelv1.Event) ([]any, error) {
	if e.GetTime() == nil {
		return nil, errors.New("time is missing")
	}
	ip, err := netip.ParseAddr(e.GetSrcIp())
	if err != nil {
		return nil, errors.New("src_ip is not an IP address")
	}
	var kind string
	var payload proto.Message
	switch p := e.GetPayload().(type) {
	case *sentinelv1.Event_Nginx:
		kind, payload = "nginx", p.Nginx
	case *sentinelv1.Event_Ssh:
		kind, payload = "ssh", p.Ssh
	default:
		return nil, errors.New("payload is missing or unknown")
	}
	data, err := payloadJSON.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []any{e.GetTime().AsTime(), e.GetHost(), ip, kind, data}, nil
}
