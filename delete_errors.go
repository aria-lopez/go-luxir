package luxir

import (
	"fmt"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// DeleteError ;; delete failed, either before reaching Luxir, while it was flyin', or Luxir said no
type DeleteError struct {
	Resp *luxirpb.UpdateResponse // ;; Luxirs answer or nil if the call failed
	Msg  string                  // ;; error msg
	Err  error                   // ;; da err
}

func (e *DeleteError) Error() string {
	switch {
	case e.Msg != "":
		return "[go-luxir] delete: " + e.Msg
	case e.Resp != nil:
		return fmt.Sprintf("[go-luxir] delete %s: %s", e.Resp.GetStatus(), e.Resp.GetError().GetMessage())
	case e.Err != nil:
		return "[go-luxir] delete: " + e.Err.Error()
	default:
		return "[go-luxir] delete error"
	}
}

func (e *DeleteError) Unwrap() error { return e.Err }

// RequestFailed ;; Luxir's request-level error
func (e *DeleteError) RequestFailed() *luxirpb.Error { return e.Resp.GetError() }
