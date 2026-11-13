package ship

import (
	"context"
	"sync"
	"testing"
	"time"

	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeIngest records batches and fails calls with the queued errors first.
type fakeIngest struct {
	mu      sync.Mutex
	errs    []error
	calls   int
	batches [][]string // src_ip of each stored event, used as an id
	stored  chan struct{}
}

func newFake(errs ...error) *fakeIngest {
	return &fakeIngest{errs: errs, stored: make(chan struct{}, 100)}
}

func (f *fakeIngest) Ship(_ context.Context, req *sentinelv1.ShipRequest, _ ...grpc.CallOption) (*sentinelv1.ShipResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	var ids []string
	for _, e := range req.GetEvents() {
		ids = append(ids, e.GetSrcIp())
	}
	f.batches = append(f.batches, ids)
	f.stored <- struct{}{}
	return &sentinelv1.ShipResponse{}, nil
}

func (f *fakeIngest) waitBatches(t *testing.T, n int) [][]string {
	t.Helper()
	for range n {
		select {
		case <-f.stored:
		case <-time.After(2 * time.Second):
			t.Fatalf("fewer than %d batches stored within 2s", n)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batches
}

func run(t *testing.T, s Shipper) (in chan *sentinelv1.Event, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	in = make(chan *sentinelv1.Event)
	done := make(chan struct{})
	go func() { s.Run(ctx, in); close(done) }()
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	return in, stop
}

func event(id string) *sentinelv1.Event { return &sentinelv1.Event{SrcIp: id} }

func equal(got [][]string, want ...[]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			return false
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				return false
			}
		}
	}
	return true
}

func TestRunShipsFullBatchesAtOnceAndTheRestOnTheTimer(t *testing.T) {
	f := newFake()
	in, _ := run(t, Shipper{Client: f, MaxBatch: 2, Interval: 50 * time.Millisecond, Backoff: time.Millisecond})

	for _, id := range []string{"a", "b", "c"} {
		in <- event(id)
	}
	if got := f.waitBatches(t, 2); !equal(got, []string{"a", "b"}, []string{"c"}) {
		t.Fatalf("batches = %v, want [a b] then [c]", got)
	}
}

func TestRunRetriesTheSameBatchUntilItIsStored(t *testing.T) {
	down := status.Error(codes.Unavailable, "down")
	f := newFake(down, down)
	in, _ := run(t, Shipper{Client: f, MaxBatch: 2, Interval: time.Hour, Backoff: time.Millisecond})

	in <- event("a")
	in <- event("b")
	if got := f.waitBatches(t, 1); !equal(got, []string{"a", "b"}) {
		t.Fatalf("batches = %v, want [a b] once", got)
	}
	if f.calls != 3 {
		t.Errorf("Ship called %d times, want 3", f.calls)
	}
}

func TestRunDropsABatchIngestWillNeverAccept(t *testing.T) {
	f := newFake(status.Error(codes.InvalidArgument, "bad"))
	in, _ := run(t, Shipper{Client: f, MaxBatch: 1, Interval: time.Hour, Backoff: time.Hour})

	in <- event("poison")
	in <- event("good")
	if got := f.waitBatches(t, 1); !equal(got, []string{"good"}) {
		t.Fatalf("batches = %v, want only [good]", got)
	}
}

func TestRunFlushesWhatItHoldsOnShutdown(t *testing.T) {
	f := newFake()
	in, stop := run(t, Shipper{Client: f, MaxBatch: 100, Interval: time.Hour, Backoff: time.Millisecond})

	in <- event("a")
	stop()
	if got := f.waitBatches(t, 1); !equal(got, []string{"a"}) {
		t.Fatalf("batches = %v, want [a]", got)
	}
}
