package luxir

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/health/grpc_health_v1"
)

// ;; ErrConnecting :: failed to connect to the client given the addr
var ErrConnecting = errors.New("[go-luxir] connecting")

type ConnectError struct {
	Addr string // ;; Addr used
	Err  error  // ;; underlying gRPC response
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("[go-luxir] connecting to %s: %v", e.Addr, e.Err)
}

func (e *ConnectError) Unwrap() error { return e.Err }

func (e *ConnectError) Is(target error) bool { return target == ErrConnecting }

// ;; ErrHealthCheck :: internal Luxir.health.Check error
var ErrHealthCheck = errors.New("[go-luxir] health check")

type HealthCheckError struct {
	Err error
}

func (e *HealthCheckError) Error() string {
	return fmt.Sprintf("[go-luxir] internal error during health check %v", e.Err)
}

func (e *HealthCheckError) Unwrap() error { return e.Err }

func (e *HealthCheckError) Is(target error) bool { return target == ErrHealthCheck }

// ;; ErrHealthCheck_SERVING ;; Made contact with node during health check request but did not get response
var ErrHealthCheck_SERVING = errors.New("[go-luxir] serving error during health check")

type HealthCheckError_SERVING struct {
	Status grpc_health_v1.HealthCheckResponse_ServingStatus // ;; what the node reported, anything but SERVING
}

func (e *HealthCheckError_SERVING) Error() string {
	return fmt.Sprintf("[go-luxir] response error during health check: node reported %s", e.Status)
}

// Is ;; matches ErrHealthCheck_SERVING, and also ErrHealthCheck since it is a kind of health check error
func (e *HealthCheckError_SERVING) Is(target error) bool {
	return target == ErrHealthCheck_SERVING || target == ErrHealthCheck
}
