package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvmvkxnt/sentinel/ingest/internal/store"
	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TEST: integration tests against real Postgres and Redis. They skip
// locally without the URLs and fail in CI, same as the store tests.
func env(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set in CI", key)
		}
		t.Skipf("%s is not set", key)
	}
	return v
}

type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newCA(t *testing.T) ca {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return ca{cert, key}
}

func (c ca) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.cert)
	return p
}

func (c ca) issue(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type fixture struct {
	addr   string
	ca     ca
	stream string
	rdb    *redis.Client
	pg     *pgx.Conn
}

// start runs an ingest server on a random local port. redisURL lets a test
// point the server at a dead Redis.
func start(t *testing.T, redisURL string) fixture {
	t.Helper()
	ctx := context.Background()
	pgURL := env(t, "SENTINEL_TEST_POSTGRES_URL")
	goodRedis := env(t, "SENTINEL_TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = goodRedis
	}

	st, err := store.Open(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	opts.MaxRetries = -1
	opts.DialTimeout = time.Second
	serverRedis := redis.NewClient(opts)
	t.Cleanup(func() { serverRedis.Close() })

	authority := newCA(t)
	stream := "test-events-" + time.Now().Format(time.RFC3339Nano)
	srv := grpc.NewServer(grpc.Creds(Credentials(authority.issue(t, "ingest"), authority.pool())))
	sentinelv1.RegisterIngestServiceServer(srv, New(st, serverRedis, stream))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis) //nolint:errcheck // stopped by the cleanup below
	t.Cleanup(srv.Stop)

	goodOpts, _ := redis.ParseURL(goodRedis)
	rdb := redis.NewClient(goodOpts)
	t.Cleanup(func() { rdb.Del(ctx, stream); rdb.Close() })
	pg, err := pgx.Connect(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close(ctx) })
	return fixture{lis.Addr().String(), authority, stream, rdb, pg}
}

func (f fixture) client(t *testing.T, certs ...tls.Certificate) sentinelv1.IngestServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(f.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs:      f.ca.pool(),
		Certificates: certs,
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return sentinelv1.NewIngestServiceClient(conn)
}

func (f fixture) stored(t *testing.T, host string) int {
	t.Helper()
	var n int
	if err := f.pg.QueryRow(context.Background(),
		"SELECT count(*) FROM events WHERE host = $1", host).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sshEvent(host string) *sentinelv1.Event {
	return &sentinelv1.Event{
		Time: timestamppb.Now(), Host: host, SrcIp: "203.0.113.7",
		Payload: &sentinelv1.Event_Ssh{Ssh: &sentinelv1.SshAuth{
			Outcome: sentinelv1.SshAuth_OUTCOME_FAILED, User: "root",
		}},
	}
}

func uniqueHost(t *testing.T) string {
	return t.Name() + "-" + time.Now().Format(time.RFC3339Nano)
}

func TestShipStoresAndPublishesUnderTheCertificateHost(t *testing.T) {
	f := start(t, "")
	ctx := context.Background()
	host := uniqueHost(t)
	client := f.client(t, f.ca.issue(t, host))

	_, err := client.Ship(ctx, &sentinelv1.ShipRequest{
		Events: []*sentinelv1.Event{sshEvent("spoofed"), sshEvent("spoofed")},
	})
	if err != nil {
		t.Fatalf("Ship: %v", err)
	}

	if n := f.stored(t, host); n != 2 {
		t.Errorf("%d events stored under the certificate host, want 2", n)
	}
	entries, err := f.rdb.XRange(ctx, f.stream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d stream entries, want 2", len(entries))
	}
	var got sentinelv1.Event
	if err := proto.Unmarshal([]byte(entries[0].Values["pb"].(string)), &got); err != nil {
		t.Fatal(err)
	}
	if got.GetHost() != host || got.GetSsh().GetUser() != "root" {
		t.Errorf("stream event = %v, want host %q and user root", &got, host)
	}
}

// Agents unmap already, but ingest does not trust agents: one client must
// be one address in the table and on the stream, or per-IP counts split.
func TestShipNormalizesIPv4MappedAddresses(t *testing.T) {
	f := start(t, "")
	ctx := context.Background()
	host := uniqueHost(t)
	e := sshEvent("")
	e.SrcIp = "::ffff:203.0.113.7"

	if _, err := f.client(t, f.ca.issue(t, host)).Ship(ctx, &sentinelv1.ShipRequest{
		Events: []*sentinelv1.Event{e},
	}); err != nil {
		t.Fatalf("Ship: %v", err)
	}

	var stored string
	if err := f.pg.QueryRow(ctx, "SELECT host(src_ip) FROM events WHERE host = $1", host).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	entries, err := f.rdb.XRange(ctx, f.stream, "-", "+").Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("stream entries = %d, err = %v", len(entries), err)
	}
	var published sentinelv1.Event
	if err := proto.Unmarshal([]byte(entries[0].Values["pb"].(string)), &published); err != nil {
		t.Fatal(err)
	}
	if stored != "203.0.113.7" || published.GetSrcIp() != "203.0.113.7" {
		t.Errorf("stored %q, published %q, want 203.0.113.7 in both", stored, published.GetSrcIp())
	}
}

func TestShipRejectsBatchesThatCanNeverBeStored(t *testing.T) {
	f := start(t, "")
	host := uniqueHost(t)
	client := f.client(t, f.ca.issue(t, host))

	bad := sshEvent("")
	bad.SrcIp = "not-an-ip"
	tooMany := make([]*sentinelv1.Event, maxBatch+1)
	for i := range tooMany {
		tooMany[i] = sshEvent("")
	}
	for name, events := range map[string][]*sentinelv1.Event{
		"invalid event": {sshEvent(""), bad},
		"oversized":     tooMany,
	} {
		_, err := client.Ship(context.Background(), &sentinelv1.ShipRequest{Events: events})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: code = %v, want InvalidArgument (%v)", name, status.Code(err), err)
		}
	}
	if n := f.stored(t, host); n != 0 {
		t.Errorf("%d events stored from rejected batches, want 0", n)
	}
	if n, _ := f.rdb.XLen(context.Background(), f.stream).Result(); n != 0 {
		t.Errorf("%d stream entries from rejected batches, want 0", n)
	}
}

func TestShipIsRetryableAndStoresNothingWhenRedisIsDown(t *testing.T) {
	f := start(t, "redis://127.0.0.1:1")
	host := uniqueHost(t)
	client := f.client(t, f.ca.issue(t, host))

	_, err := client.Ship(context.Background(), &sentinelv1.ShipRequest{
		Events: []*sentinelv1.Event{sshEvent("")},
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable (%v)", status.Code(err), err)
	}
	if n := f.stored(t, host); n != 0 {
		t.Errorf("%d events stored while the stream was down, want 0", n)
	}
}

func TestShipNeedsAClientCertificateFromOurCA(t *testing.T) {
	f := start(t, "")
	req := &sentinelv1.ShipRequest{Events: []*sentinelv1.Event{sshEvent("")}}

	if _, err := f.client(t).Ship(context.Background(), req); err == nil {
		t.Error("Ship without a client certificate succeeded")
	}
	stranger := newCA(t).issue(t, "intruder")
	if _, err := f.client(t, stranger).Ship(context.Background(), req); err == nil {
		t.Error("Ship with a certificate from another CA succeeded")
	}
	if n := f.stored(t, "intruder"); n != 0 {
		t.Errorf("%d events stored from an untrusted client, want 0", n)
	}
}
