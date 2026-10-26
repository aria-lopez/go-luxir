package luxir

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrCollectionExists   = errors.New("[go-luxir] collection already exists")
	ErrCollectionNotFound = errors.New("[go-luxir] collection not found")
)

// CollectionError ;; a collection admin call failed
type CollectionError struct {
	Op   string // ;; create, delete, list, stats, get schema, set schema, replace schema, cache control
	Name string // ;; collection, "" for list
	Msg  string // ;; readable detail for internal error
	Err  error  // ;; underlying gRPC error
}

func (e *CollectionError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("[go-luxir] collection %s: %s", e.Op, e.Msg)
	}
	if e.Name == "" {
		return fmt.Sprintf("[go-luxir] collection %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("[go-luxir] collection %s %q: %v", e.Op, e.Name, e.Err)
}

func (e *CollectionError) Unwrap() error { return e.Err }

// Is ;; maps gRPC status codes onto the Err* sentinels
func (e *CollectionError) Is(target error) bool {
	switch status.Code(e.Err) {
	case codes.AlreadyExists:
		return target == ErrCollectionExists
	case codes.NotFound:
		return target == ErrCollectionNotFound
	}
	return false
}
