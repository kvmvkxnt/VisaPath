package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TEST: needs a real Postgres. Locally the tests skip without
// SENTINEL_TEST_POSTGRES_URL; in CI a missing URL is a failure, so the suite
// can never pass by skipping.
func open(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("SENTINEL_TEST_POSTGRES_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("SENTINEL_TEST_POSTGRES_URL is not set in CI")
		}
		t.Skip("SENTINEL_TEST_POSTGRES_URL is not set")
	}
	s, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func noPublish(context.Context) error { return nil }

func batch(host string) []*sentinelv1.Event {
	at := timestamppb.New(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	return []*sentinelv1.Event{
		{
			Time: at, Host: host, SrcIp: "203.0.113.7",
			Payload: &sentinelv1.Event_Nginx{Nginx: &sentinelv1.NginxAccess{
				Method: "GET", Path: "/wp-login.php", Status: 404, UserAgent: "curl/8",
			}},
		},
		{
			Time: at, Host: host, SrcIp: "2001:db8::1",
			Payload: &sentinelv1.Event_Ssh{Ssh: &sentinelv1.SshAuth{
				Outcome: sentinelv1.SshAuth_OUTCOME_FAILED, User: "root",
			}},
		},
	}
}

func count(t *testing.T, s *Store, host string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM events WHERE host = $1", host).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInsertStoresBatch(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	host := t.Name() + time.Now().Format(time.RFC3339Nano)

	if err := s.Insert(ctx, batch(host), noPublish); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT kind, host(src_ip), time, coalesce(data->>'path', data->>'user')
		 FROM events WHERE host = $1 ORDER BY id`, host)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		kind, ip string
		at       time.Time
		detail   string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.kind, &r.ip, &r.at, &r.detail); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	want := []row{
		{"nginx", "203.0.113.7", at, "/wp-login.php"},
		{"ssh", "2001:db8::1", at, "root"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].kind != want[i].kind || got[i].ip != want[i].ip ||
			!got[i].at.Equal(want[i].at) || got[i].detail != want[i].detail {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestInsertRollsBackWhenPublishFails(t *testing.T) {
	s := open(t)
	host := t.Name() + time.Now().Format(time.RFC3339Nano)
	boom := errors.New("redis down")

	err := s.Insert(context.Background(), batch(host),
		func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Insert error = %v, want %v", err, boom)
	}
	if n := count(t, s, host); n != 0 {
		t.Fatalf("%d events stored after a failed publish, want 0", n)
	}
}

func TestInsertRejectsWholeBatchOnBadEvent(t *testing.T) {
	s := open(t)
	host := t.Name() + time.Now().Format(time.RFC3339Nano)

	for name, breakIt := range map[string]func(*sentinelv1.Event){
		"bad ip":     func(e *sentinelv1.Event) { e.SrcIp = "not-an-ip" },
		"no payload": func(e *sentinelv1.Event) { e.Payload = nil },
		"no time":    func(e *sentinelv1.Event) { e.Time = nil },
	} {
		events := batch(host)
		breakIt(events[1])
		err := s.Insert(context.Background(), events, noPublish)
		if !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("%s: Insert error = %v, want ErrInvalidEvent", name, err)
		}
	}
	if n := count(t, s, host); n != 0 {
		t.Fatalf("%d events stored from rejected batches, want 0", n)
	}
}
