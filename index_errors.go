package luxir

import (
	"fmt"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// IndexError ;; index call failed, either in before reaching Luxir, while it was flyin', or specific document(s) failed
type IndexError struct {
	Resp *luxirpb.UpdateResponse // ;; Luxir's answer, nil if the call never got one
	Msg  string                  // ;; readable detail for internal error
	Err  error                   // ;; underlying cause, nil for per-document fails
}

// Error ;; err impl
func (e *IndexError) Error() string {
	switch {
	case e.Msg != "":
		return "[go-luxir] index: " + e.Msg
	case e.Resp.GetError() != nil:
		return fmt.Sprintf("[go-luxir] index %s: %s", e.Resp.GetStatus(), e.Resp.GetError().GetMessage())
	case e.Resp != nil:
		return fmt.Sprintf("[go-luxir] index %s: %d document(s) failed", e.Resp.GetStatus(), e.Resp.GetTotalErrors())
	case e.Err != nil:
		return "[go-luxir] index: " + e.Err.Error()
	default:
		return "[go-luxir] index error"
	}
}

// Unwrap ;; lets errors.Is / errors.As see the underlying cause
func (e *IndexError) Unwrap() error { return e.Err }

// InternalError ;; code error
// ;; returns Msg or "" if there is no internal error
func (e *IndexError) InternalError() string { return e.Msg }

// Status ;; PARTIAL or ERROR
func (e *IndexError) Status() luxirpb.UpdateResponse_Status { return e.Resp.GetStatus() }

// IsPartial ;; Status is PARTIAL
func (e *IndexError) IsPartial() bool { return e.Resp.GetStatus() == luxirpb.UpdateResponse_PARTIAL }

// Failed ;; collection of per-document fails (id, position, reason)
func (e *IndexError) Failed() []*luxirpb.UpdateResponse_DocError { return e.Resp.GetErrors() }

// RequestFailed ;; the request-level error, or just nil if the fail was per-document
func (e *IndexError) RequestFailed() *luxirpb.Error { return e.Resp.GetError() }
