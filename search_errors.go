package luxir

import (
	"github.com/aria-lopez/go-luxir/luxirpb"
)

// MatchError ;; error during Luxir.Match()
type MatchError struct {
	Err error
	Msg string
}

// Error ;;
func (e *MatchError) Error() string {
	if e.Msg == "" && e.Err != nil {
		return "[go-luxir] match: " + e.Err.Error()
	}
	return e.Msg
}

// Unwrap ;; lets errors.Is / errors.As see the underlying cause
func (e *MatchError) Unwrap() error { return e.Err }

// SearchError ;; search failed, either before reaching Luxir, while it was flyin', or Luxir said no
type SearchError struct {
	Luxir *luxirpb.Error // ;; Luxir's rejection (kind, code, message), nil if it never answered
	Msg   string         // ;; readable detail for internal error
	Err   error          // ;; underlying cause, nil when Luxir itself rejected the search
}

// Error ;; err impl
func (e *SearchError) Error() string {
	switch {
	case e.Msg != "":
		return "[go-luxir] search: " + e.Msg
	case e.Luxir != nil:
		return "[go-luxir] search " + e.Luxir.GetKind().String() + " (" + e.Luxir.GetCode() + "): " + e.Luxir.GetMessage()
	case e.Err != nil:
		return "[go-luxir] search: " + e.Err.Error()
	default:
		return "[go-luxir] search error"
	}
}

// Unwrap ;; lets errors.Is / errors.As see the underlying cause
func (e *SearchError) Unwrap() error { return e.Err }

// RequestFailed ;; Luxir's error (kind, code, message), or nil if the search never got an answer
//
// ;; Kind tells you who must act, INVALID_REQUEST means fix the query, UNAVAILABLE means retry
func (e *SearchError) RequestFailed() *luxirpb.Error { return e.Luxir }
