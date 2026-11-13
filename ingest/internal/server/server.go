// Package server implements the IngestService gRPC API.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/netip"

	"github.com/kvmvkxnt/sentinel/ingest/internal/store"
	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	// maxBatch bounds how much one Ship call can hold in a transaction.
	maxBatch = 1000
	// NOTE: the stream is a bus, not storage; Postgres keeps everything.
	// ~1M entries is hours of backlog for a slow detector at demo volume.
	streamMaxLen = 1_000_000
)

// Credentials returns server transport credentials that require a client
// certificate signed by clientCAs.
func Credentials(cert tls.Certificate, clientCAs *x509.CertPool) credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	})
}

// Server stores shipped batches and publishes them to a Redis stream.
type Server struct {
	sentinelv1.UnimplementedIngestServiceServer
	store  *store.Store
	redis  *redis.Client
	stream string
}

// New returns a Server that publishes to the named Redis stream.
func New(st *store.Store, rdb *redis.Client, stream string) *Server {
	return &Server{store: st, redis: rdb, stream: stream}
}

// Ship stores and publishes one batch, whole or not at all. InvalidArgument
// means the batch can never be accepted; any other error is safe to retry.
func (s *Server) Ship(ctx context.Context, req *sentinelv1.ShipRequest) (*sentinelv1.ShipResponse, error) {
	host, err := certHost(ctx)
	if err != nil {
		return nil, err
	}
	events := req.GetEvents()
	if len(events) > maxBatch {
		return nil, status.Errorf(codes.InvalidArgument, "batch has %d events, the limit is %d", len(events), maxBatch)
	}
	if len(events) == 0 {
		return &sentinelv1.ShipResponse{}, nil
	}
	for _, e := range events {
		if e == nil {
			return nil, status.Error(codes.InvalidArgument, "batch has an empty event")
		}
		// The agent's own idea of its name is not trusted.
		e.Host = host
		// ::ffff:1.2.3.4 and 1.2.3.4 are one client. An address that does
		// not parse is left for the store to reject.
		if ip, err := netip.ParseAddr(e.GetSrcIp()); err == nil {
			e.SrcIp = ip.Unmap().String()
		}
	}

	err = s.store.Insert(ctx, events, func(ctx context.Context) error {
		_, err := s.redis.TxPipelined(ctx, func(p redis.Pipeliner) error {
			for _, e := range events {
				pb, err := proto.Marshal(e)
				if err != nil {
					return err
				}
				p.XAdd(ctx, &redis.XAddArgs{
					Stream: s.stream,
					MaxLen: streamMaxLen,
					Approx: true,
					Values: map[string]any{"pb": pb},
				})
			}
			return nil
		})
		return err
	})
	switch {
	case err == nil:
		return &sentinelv1.ShipResponse{}, nil
	case errors.Is(err, store.ErrInvalidEvent):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	default:
		// The cause can name internal addresses; the agent only needs to
		// know it may retry.
		slog.ErrorContext(ctx, "ship failed", "host", host, "events", len(events), "err", err)
		return nil, status.Error(codes.Unavailable, "batch not stored, retry later")
	}
}

// certHost returns the common name of the verified client certificate.
func certHost(ctx context.Context) (string, error) {
	p, _ := peer.FromContext(ctx)
	if p != nil {
		if info, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(info.State.VerifiedChains) > 0 {
			if cn := info.State.VerifiedChains[0][0].Subject.CommonName; cn != "" {
				return cn, nil
			}
		}
	}
	return "", status.Error(codes.Unauthenticated, "a client certificate with a common name is required")
}
