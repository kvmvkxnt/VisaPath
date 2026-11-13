// Package store writes events to Postgres.
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
//
// NOTE: if the commit itself fails after a successful publish, the resend
// publishes the batch a second time. Stream consumers see at-least-once.
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
	if err := e.GetTime().CheckValid(); err != nil {
		return nil, err
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
	case *sentinelv1.Event_Xray:
		kind, payload = "xray", p.Xray
	default:
		return nil, errors.New("payload is missing or unknown")
	}
	data, err := payloadJSON.Marshal(withoutNUL(payload))
	if err != nil {
		return nil, err
	}
	return []any{e.GetTime().AsTime(), replaceNUL(e.GetHost()), ip, kind, data}, nil
}

// Postgres refuses NUL in text and jsonb, and log strings are
// attacker-controlled, so a NUL becomes U+FFFD instead of failing the batch.
func replaceNUL(s string) string { return strings.ReplaceAll(s, "\x00", "\ufffd") }

// withoutNUL returns m, or a cleaned copy when a string field holds a NUL.
// Payload messages are flat, so top-level string fields are all there is.
func withoutNUL(m proto.Message) proto.Message {
	dirty := false
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		dirty = fd.Kind() == protoreflect.StringKind && strings.ContainsRune(v.String(), 0)
		return !dirty
	})
	if !dirty {
		return m
	}
	c := proto.Clone(m).ProtoReflect()
	c.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() == protoreflect.StringKind {
			c.Set(fd, protoreflect.ValueOfString(replaceNUL(v.String())))
		}
		return true
	})
	return c.Interface()
}
