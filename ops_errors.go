package luxir

import "fmt"

// OpError ;; an op (facet, metric, fusion, ...) was built wrong, found before talking to Luxir
type OpError struct {
	Op  string // ;; the name it was given, "" if it has none yet
	Msg string // ;; readable detail
	Err error  // ;; underlying cause
}

func (e *OpError) Error() string {
	msg := e.Msg
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Op == "" {
		return "[go-luxir] op: " + msg
	}
	return fmt.Sprintf("[go-luxir] op %q: %s", e.Op, msg)
}

func (e *OpError) Unwrap() error { return e.Err }
