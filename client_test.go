package luxir

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
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
