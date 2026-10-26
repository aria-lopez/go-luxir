package luxir

import "fmt"

// SchemaError ;; one thing wrong with a Schema, found before talking to Luxir
type SchemaError struct {
	Name string // ;; field or template, "" when there isn't one
	Msg  string
}

func (e *SchemaError) Error() string {
	if e.Name == "" {
		return "[go-luxir] schema: " + e.Msg
	}
	return fmt.Sprintf("[go-luxir] schema %q: %s", e.Name, e.Msg)
}
