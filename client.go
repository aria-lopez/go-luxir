package luxir

import (
	"context"
	"crypto/tls"

	"github.com/aria-lopez/go-luxir/luxirpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// DefaultAddr ;; Luxirs gRPC server ADDR
const DefaultAddr = "localhost:9401"

// Luxir ;;
type Luxir struct {
	conn     *grpc.ClientConn            // ;; gRPC client
	health   grpc_health_v1.HealthClient // ;; gRPC health client
	indexer  luxirpb.IndexerClient       // ;; luxir client for indexing documents
	searcher luxirpb.SearcherClient      // ;; luxir client for searching documents
	admin    luxirpb.AdminClient         // ;; luxir client for managing collections
}

// clientConfig ;; what ClientOption edits
type clientConfig struct {
	tls     *tls.Config
	maxRecv int
	dial    []grpc.DialOption
}

// ClientOption ;; edit how NewLuxir connects
type ClientOption func(*clientConfig)

// WithTLS ;; connect over TLS, Luxir has none built in so this is for a TLS proxy or mesh in front of it
func WithTLS(cfg *tls.Config) ClientOption {
	return func(c *clientConfig) {
		if cfg == nil {
			cfg = &tls.Config{}
		}
		c.tls = cfg
	}
}

// WithMaxMessageSize ;; biggest response message accepted in bytes, gRPC's default is 4 MiB
func WithMaxMessageSize(n int) ClientOption {
	return func(c *clientConfig) { c.maxRecv = n }
}

// WithDialOptions ;; extra gRPC dial options, ex grpc.WithPerRPCCredentials for an auth proxy
func WithDialOptions(opts ...grpc.DialOption) ClientOption {
	return func(c *clientConfig) { c.dial = append(c.dial, opts...) }
}

// NewLuxir ;; Get a new instance of *Luxir by the provided addr
//
// ;; takes in addr to Luxir's gRPC server default is localhost:9401
// ;; plaintext unless WithTLS, Luxir has no auth or TLS so keep it on a private network
// ;; returns *ConnectError on connection error
func NewLuxir(addr string, opts ...ClientOption) (*Luxir, error) {
	if addr == "" {
		addr = DefaultAddr
	}
	var cfg clientConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}

	creds := insecure.NewCredentials()
	if cfg.tls != nil {
		creds = credentials.NewTLS(cfg.tls)
	}
	dial := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if cfg.maxRecv > 0 {
		dial = append(dial, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(cfg.maxRecv)))
	}
	dial = append(dial, cfg.dial...)

	conn, err := grpc.NewClient(addr, dial...)
	if err != nil {
		return nil, &ConnectError{Addr: addr, Err: err}
	}

	return &Luxir{
		conn:     conn,
		health:   grpc_health_v1.NewHealthClient(conn),
		indexer:  luxirpb.NewIndexerClient(conn),
		searcher: luxirpb.NewSearcherClient(conn),
		admin:    luxirpb.NewAdminClient(conn),
	}, nil
}

// HealthCheck ;; sends a HealthCheckRequest @Luxir.addr
//
// ;; returns *HealthCheckError internal error talking to Luxir.health
// ;; returns *HealthCheckError_SERVING if we made contact but did not get a good response
func (l *Luxir) HealthCheck(ctx context.Context) error {
	resp, err := l.health.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return &HealthCheckError{Err: err}
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return &HealthCheckError_SERVING{Status: resp.GetStatus()}
	}
	return nil
}

// Close ;; closes *Luxir.conn
//
// ;; defer *Luxir.Close() when ya make one.
func (l *Luxir) Close() error {
	return l.conn.Close()
}
