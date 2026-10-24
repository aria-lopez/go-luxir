package luxir

import (
	"errors"
	"fmt"
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
	Err error
}

func (e *HealthCheckError_SERVING) Error() string {
	return fmt.Sprintf("[go-luxir] response error during health check %v", e.Err)
}

func (e *HealthCheckError_SERVING) Unwrap() error { return e.Err }

func (e *HealthCheckError_SERVING) Is(target error) bool { return target == ErrHealthCheck }
