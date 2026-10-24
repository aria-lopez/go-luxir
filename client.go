package luxir

import (
	"context"

	"github.com/aria-lopez/go-luxir/luxirpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// DefaultAddr ;; Luxirs gRPC server ADDR
const DefaultAddr = "localhost:9401"

// Luxir ;;
type Luxir struct {
	conn    *grpc.ClientConn
	health  grpc_health_v1.HealthClient
	indexer luxirpb.IndexerClient
}

// NewLuxir ;; Get a new instance of *Luxir by the provided addr
//
// ;; takes in addr to Luxir's gRPC server default is localhost:9401
// ;; returns *ConnectError on connection error
func NewLuxir(addr string) (*Luxir, error) {
	if addr == "" {
		addr = DefaultAddr
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, &ConnectError{Addr: addr, Err: err}
	}

	return &Luxir{
		conn:    conn,
		health:  grpc_health_v1.NewHealthClient(conn),
		indexer: luxirpb.NewIndexerClient(conn),
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
		return &HealthCheckError_SERVING{Err: err}
	}
	return nil
}

// Close ;; closes *Luxir.conn ;; FIXME: [easy] [test] [no-coverage]
//
// ;; defer *Luxir.Close() when ya make one.
func (l *Luxir) Close() error {
	return l.conn.Close()
}
