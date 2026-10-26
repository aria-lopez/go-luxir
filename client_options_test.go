package luxir

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func tlsHealthServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})))
	grpc_health_v1.RegisterHealthServer(srv, health.NewServer())
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func shortCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func TestNewLuxir_TLS(t *testing.T) {
	cert, pool := selfSigned(t)
	addr := tlsHealthServer(t, cert)

	tests := []struct {
		name string
		opts []ClientOption
		ok   bool
	}{
		{"trusted", []ClientOption{WithTLS(&tls.Config{RootCAs: pool})}, true},
		{"untrusted", []ClientOption{WithTLS(nil)}, false},
		{"plaintext", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := NewLuxir(addr, tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			if err := l.HealthCheck(shortCtx(t)); (err == nil) != tt.ok {
				t.Errorf("HealthCheck = %v, want ok = %v", err, tt.ok)
			}
		})
	}
}

func TestNewLuxir_DialOptions(t *testing.T) {
	var calls atomic.Int32
	count := grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		calls.Add(1)
		return invoker(ctx, method, req, reply, cc, opts...)
	})
	l := fakeServer(t, func(s *grpc.Server) { grpc_health_v1.RegisterHealthServer(s, health.NewServer()) }, WithDialOptions(count), nil)

	if err := l.HealthCheck(timeout(t)); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("interceptor saw %d calls, want 1", calls.Load())
	}
}

func TestNewLuxir_MaxMessageSize(t *testing.T) {
	big := docList("x")
	big.Docs[0].Fields["body_s"] = &luxirpb.Val{Kind: &luxirpb.Val_S{S: strings.Repeat("x", 4096)}}
	reply := fakeReply(hitsResp(false, big))
	register := func(s *grpc.Server) { luxirpb.RegisterSearcherServer(s, &fakeSearcher{reply: reply}) }

	small := fakeServer(t, register, WithMaxMessageSize(1024))
	if _, err := small.Search(timeout(t), "c", nil); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("err = %v, want ResourceExhausted over the limit", err)
	}

	roomy := fakeServer(t, register, WithMaxMessageSize(1<<20))
	if _, err := roomy.Search(timeout(t), "c", nil); err != nil {
		t.Errorf("err = %v, want the big response accepted", err)
	}
}

func TestNewLuxir_BadAddr(t *testing.T) {
	_, err := NewLuxir("%zz")

	var ce *ConnectError
	if !errors.As(err, &ce) || ce.Addr != "%zz" || !errors.Is(err, ErrConnecting) || errors.Unwrap(err) == nil {
		t.Fatalf("err = %v, want *ConnectError matching ErrConnecting with a cause", err)
	}
	if !strings.Contains(err.Error(), "connecting to %zz") {
		t.Errorf("Error() = %q", err)
	}
}

func TestClose(t *testing.T) {
	l, err := NewLuxir("127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := l.HealthCheck(context.Background()); status.Code(err) != codes.Canceled {
		t.Errorf("HealthCheck after Close = %v, want Canceled", err)
	}
}

func TestHealthCheckError(t *testing.T) {
	cause := errors.New("boom")
	err := error(&HealthCheckError{Err: cause})

	if !errors.Is(err, ErrHealthCheck) || !errors.Is(err, cause) || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want ErrHealthCheck, the cause and its text", err)
	}
}
