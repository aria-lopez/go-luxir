package luxir

import "fmt"

// QueryError ;; a query helper was given something Luxir can't take, found before talking to Luxir
type QueryError struct {
	Query string // ;; helper, ex "fuzzy"
	Msg   string // ;; readable detail
	Err   error  // ;; underlying cause, nil for plain bad input
}

func (e *QueryError) Error() string {
	if e.Msg == "" && e.Err != nil {
		return fmt.Sprintf("[go-luxir] %s query: %v", e.Query, e.Err)
	}
	return fmt.Sprintf("[go-luxir] %s query: %s", e.Query, e.Msg)
}

func (e *QueryError) Unwrap() error { return e.Err }

// queryErr ;; *QueryError from a format string
func queryErr(query, format string, a ...any) error {
	return &QueryError{Query: query, Msg: fmt.Sprintf(format, a...)}
}
