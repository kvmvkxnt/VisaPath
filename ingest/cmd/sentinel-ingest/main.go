// Command sentinel-ingest receives event batches from agents over gRPC with
// mTLS, stores them in Postgres and publishes them to a Redis stream.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kvmvkxnt/sentinel/ingest/internal/server"
	"github.com/kvmvkxnt/sentinel/ingest/internal/store"
	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		// For the container healthcheck: the image has no shell or curl.
		// NOTE: only proves the port is open, which happens after the
		// schema is applied. Good enough until there is a status RPC.
		conn, err := net.DialTimeout("tcp", os.Getenv("INGEST_LISTEN"), 2*time.Second)
		if err != nil {
			os.Exit(1)
		}
		conn.Close()
		return
	}
	if err := run(); err != nil {
		slog.Error("sentinel-ingest stopped", "err", err)
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
	postgresURL, redisURL, listen := env("POSTGRES_URL"), env("REDIS_URL"), env("INGEST_LISTEN")
	certFile, keyFile, caFile := env("INGEST_TLS_CERT"), env("INGEST_TLS_KEY"), env("INGEST_TLS_CLIENT_CA")
	if len(missing) > 0 {
		return fmt.Errorf("not set: %s", strings.Join(missing, ", "))
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load server certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return errors.New("client CA file holds no certificates")
	}

	st, err := store.Open(ctx, postgresURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer st.Close()
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	srv := grpc.NewServer(grpc.Creds(server.Credentials(cert, clientCAs)))
	sentinelv1.RegisterIngestServiceServer(srv, server.New(st, rdb, "events"))
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	slog.Info("sentinel-ingest listening", "addr", lis.Addr().String())
	return srv.Serve(lis)
}
