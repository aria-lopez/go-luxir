package luxir

// FIXME: these need to be full errors im just a bit lazy rn.

// MatchError ;; error during Luxir.Match()
type MatchError struct {
	Err error
	Msg string
}

// Error ;;
func (e *MatchError) Error() string {
	return e.Msg
}

func (e *MatchError) Unwrap() error { return e.Err }

// SearchError ;; all for all errors through Luxir.Search()
type SearchError struct {
	Err error
	Msg string
}

func (e *SearchError) Error() string { return e.Msg }
func (e *SearchError) Unwrap() error { return e.Err }
