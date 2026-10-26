package luxir

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func getClient(t *testing.T) *Luxir {
	t.Helper()
	addr := os.Getenv("LUXIR_ADDR")
	if addr == "" {
		addr = DefaultAddr
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err != nil {
		t.Skipf("no Luxir running @%s: %v", addr, err)
	} else {
		c.Close()
	}
	l, err := NewLuxir(addr)
	if err != nil {
		t.Fatalf("NewLuxir(%q): %v", addr, err)
	}

	t.Cleanup(func() { l.conn.Close() })
	return l
}

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHealthCheck(t *testing.T) {
	l := getClient(t)
	if err := l.HealthCheck(timeout(t)); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestHealthCheck_Unreachable(t *testing.T) {
	// free port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()

	l, err := NewLuxir(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.conn.Close() })
	err = l.HealthCheck(timeout(t))

	var he *HealthCheckError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v (%T), want *HealthCheckError", err, err)
	}
	if !errors.Is(err, ErrHealthCheck) {
		t.Error("errors.Is(err, ErrHealthCheck) = false")
	}
}

// TestHealthCheck_NotServing ;; node answers but says it is not serving: *HealthCheckError_SERVING with the status
//
// ;; needs no Luxir, a stand-in health server on a free port reports NOT_SERVING
func TestHealthCheck_NotServing(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := health.NewServer()
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, hs)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	l, err := NewLuxir(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.conn.Close() })

	err = l.HealthCheck(timeout(t))

	var se *HealthCheckError_SERVING
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *HealthCheckError_SERVING", err, err)
	}
	if se.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Errorf("Status = %s, want NOT_SERVING", se.Status)
	}
	if !errors.Is(err, ErrHealthCheck_SERVING) || !errors.Is(err, ErrHealthCheck) {
		t.Error("want errors.Is true for both ErrHealthCheck_SERVING and ErrHealthCheck")
	}
	if got := err.Error(); !strings.Contains(got, "NOT_SERVING") {
		t.Errorf("err = %q, want it to say what the node reported", got)
	}
}

func TestNewLuxir_EmptyAddrIsDefault(t *testing.T) {
	l, err := NewLuxir("")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := l.conn.Target(); got != DefaultAddr {
		t.Errorf("target = %q, want %q", got, DefaultAddr)
	}
}
