// Command sentinel-agent tails the logs of one host and ships the events to
// sentinel-ingest over gRPC with mTLS.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kvmvkxnt/sentinel/agent/internal/parse"
	"github.com/kvmvkxnt/sentinel/agent/internal/ship"
	"github.com/kvmvkxnt/sentinel/agent/internal/tail"
	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sentinel-agent stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var missing []string
	env := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}
	addr := env("AGENT_INGEST_ADDR")
	certFile, keyFile, caFile := env("AGENT_TLS_CERT"), env("AGENT_TLS_KEY"), env("AGENT_TLS_CA")
	if len(missing) > 0 {
		return fmt.Errorf("not set: %s", strings.Join(missing, ", "))
	}
	// Each log is optional, but an agent that reads nothing is a mistake.
	logs := map[string]parse.Func{}
	if p := os.Getenv("AGENT_NGINX_LOG"); p != "" {
		logs[p] = parse.Nginx
	}
	if p := os.Getenv("AGENT_SSH_LOG"); p != "" {
		logs[p] = parse.SSH
	}
	if len(logs) == 0 {
		return errors.New("set AGENT_NGINX_LOG or AGENT_SSH_LOG")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load agent certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return errors.New("CA file holds no certificates")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		MinVersion:   tls.VersionTLS13,
	})))
	if err != nil {
		return err
	}
	defer conn.Close()

	events := make(chan *sentinelv1.Event)
	var wg sync.WaitGroup
	errs := make(chan error, len(logs))
	for path, parser := range logs {
		slog.Info("following", "path", path)
		wg.Go(func() {
			errs <- tail.Follow(ctx, path, 500*time.Millisecond, func(line string) {
				if e, ok := parser(line, time.Now()); ok {
					select {
					case events <- e:
					case <-ctx.Done():
					}
				}
			})
			// One dead tailer stops the agent; systemd restarts it.
			stop()
		})
	}

	ship.Shipper{
		Client:   sentinelv1.NewIngestServiceClient(conn),
		MaxBatch: 500,
		Interval: time.Second,
		Backoff:  time.Second,
	}.Run(ctx, events)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
