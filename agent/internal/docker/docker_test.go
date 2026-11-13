package docker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
)

// daemon is a fake Docker daemon on a unix socket. Each connection to
// /events gets the next scripted response, then the connection is closed.
type daemon struct {
	mu        sync.Mutex
	responses []string
	queries   []string
}

func serve(t *testing.T, responses ...string) (socket string, d *daemon) {
	t.Helper()
	// Not t.TempDir(): unix socket paths are limited to ~100 bytes.
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "docker.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	d = &daemon{responses: responses}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.queries = append(d.queries, r.URL.Path+"?"+r.URL.Query().Encode())
		body := ""
		if len(d.responses) > 0 {
			body, d.responses = d.responses[0], d.responses[1:]
		}
		d.mu.Unlock()
		fmt.Fprint(w, body)
	})}
	go srv.Serve(lis) //nolint:errcheck // closed by the cleanup below
	t.Cleanup(func() { srv.Close() })
	return socket, d
}

func collect(t *testing.T, socket string, n int) []*sentinelv1.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan *sentinelv1.Event, 100)
	go Follow(ctx, socket, time.Millisecond, func(e *sentinelv1.Event) { events <- e })
	var got []*sentinelv1.Event
	for range n {
		select {
		case e := <-events:
			got = append(got, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("got %d events within 2s, want %d", len(got), n)
		}
	}
	return got
}

const dieEvent = `{"Type":"container","Action":"die","Actor":{"ID":"abc123","Attributes":{"exitCode":"137","image":"nginx:1.29","name":"web"}},"time":1794577889,"timeNano":1794577889066977930}` + "\n"

func TestFollowTurnsContainerEventsIntoEvents(t *testing.T) {
	socket, d := serve(t, dieEvent)

	e := collect(t, socket, 1)[0]
	got := e.GetDocker()
	if got.GetAction() != "die" || got.GetContainerId() != "abc123" || got.GetContainerName() != "web" ||
		got.GetImage() != "nginx:1.29" || got.GetExitCode() != 137 {
		t.Errorf("docker event = %v", got)
	}
	if !e.GetTime().AsTime().Equal(time.Unix(0, 1794577889066977930)) || e.GetSrcIp() != "" {
		t.Errorf("time = %v, src_ip = %q", e.GetTime().AsTime(), e.GetSrcIp())
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	want := `/events?filters=` // only the lifecycle actions, only containers
	if q := d.queries[0]; !strings.HasPrefix(q, want) {
		t.Errorf("query = %q, want it to start with %q", q, want)
	}
}

// Docker restarts, the stream ends. The collector must come back and ask
// only for what it has not seen.
func TestFollowReconnectsFromTheLastEvent(t *testing.T) {
	second := `{"Type":"container","Action":"start","Actor":{"ID":"def456","Attributes":{"image":"redis:8","name":"cache"}},"timeNano":1794577899000000000}` + "\n"
	socket, d := serve(t, dieEvent, second)

	got := collect(t, socket, 2)
	if got[1].GetDocker().GetContainerId() != "def456" {
		t.Errorf("second event = %v", got[1])
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.queries) < 2 {
		t.Fatalf("%d connections, want at least 2", len(d.queries))
	}
	if want := "since=1794577889.066977931"; !strings.Contains(d.queries[1], want) {
		t.Errorf("reconnect query = %q, want %q in it", d.queries[1], want)
	}
}

func TestFollowSkipsWhatItCannotUse(t *testing.T) {
	junk := "not json\n" +
		`{"Type":"container","Action":"exec_start: sh","Actor":{"ID":"x"},"timeNano":1}` + "\n" +
		`{"Type":"network","Action":"connect","Actor":{"ID":"y"},"timeNano":2}` + "\n"
	socket, _ := serve(t, junk+dieEvent)

	if got := collect(t, socket, 1); got[0].GetDocker().GetAction() != "die" {
		t.Errorf("first event = %v, want the die event", got[0])
	}
}
