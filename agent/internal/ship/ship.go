// Package ship batches events and delivers them to ingest.
package ship

import (
	"context"
	"log/slog"
	"time"

	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxBackoff = 30 * time.Second
	// How long a shutdown waits for the last batch.
	flushTimeout = 5 * time.Second
)

// Shipper sends events to ingest in batches. All fields are required.
type Shipper struct {
	// Client is the ingest connection.
	Client sentinelv1.IngestServiceClient
	// MaxBatch is the size at which a batch is sent without waiting.
	MaxBatch int
	// Interval is how long an incomplete batch may wait.
	Interval time.Duration
	// Backoff is the first retry delay; it doubles up to 30 seconds.
	Backoff time.Duration
}

// Run ships events from in until ctx is done, then makes one last attempt
// to send what it still holds.
//
// While ingest is unreachable Run retries the same batch and stops reading
// from in. That blocks the tailers, so the backlog stays in the log files
// instead of in memory.
func (s Shipper) Run(ctx context.Context, in <-chan *sentinelv1.Event) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	var batch []*sentinelv1.Event
	for {
		select {
		case e := <-in:
			batch = append(batch, e)
			if len(batch) < s.MaxBatch {
				continue
			}
		case <-ticker.C:
		case <-ctx.Done():
			if len(batch) > 0 {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
				defer cancel()
				if err := s.send(ctx, batch); err != nil {
					slog.Error("events lost at shutdown", "events", len(batch), "err", err)
				}
			}
			return
		}
		if len(batch) > 0 && s.deliver(ctx, batch) {
			batch = nil
		}
	}
}

// deliver retries until the batch is stored or dropped. It returns false
// only when ctx ended first, leaving the batch for the shutdown flush.
func (s Shipper) deliver(ctx context.Context, batch []*sentinelv1.Event) bool {
	for delay := s.Backoff; ; delay = min(delay*2, maxBackoff) {
		err := s.send(ctx, batch)
		if err == nil {
			return true
		}
		if status.Code(err) == codes.InvalidArgument {
			// Ingest will never take this batch; resending would block
			// every later event behind it.
			slog.Error("batch rejected, dropped", "events", len(batch), "err", err)
			return true
		}
		slog.Warn("ship failed, retrying", "events", len(batch), "in", delay, "err", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
	}
}

func (s Shipper) send(ctx context.Context, batch []*sentinelv1.Event) error {
	_, err := s.Client.Ship(ctx, &sentinelv1.ShipRequest{Events: batch})
	return err
}
