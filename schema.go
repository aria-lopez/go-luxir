package luxir

import (
	"errors"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// FieldOption ;; change a field in schema
type FieldOption func(*luxirpb.FieldDef)

// Column ;; store the values in a per-field col (sorting, faceting, range, etc)
func Column() FieldOption {
	return func(f *luxirpb.FieldDef) { f.Column = ptr(true) }
}

// Multi ;; field takes a list of values
func Multi() FieldOption {
	return func(f *luxirpb.FieldDef) { f.Multi = ptr(true) }
}

// Range ;; numeric/geo range queries use an index, this implies Column
func Range() FieldOption {
	return func(f *luxirpb.FieldDef) {
		f.Index = ptr(luxirpb.FieldDef_RANGE)
		f.Column = ptr(true)
	}
}

// Unindexed ;; not searchable by term, still stored (and in a column if Column is set)
func Unindexed() FieldOption {
	return func(f *luxirpb.FieldDef) { f.Index = ptr(luxirpb.FieldDef_NONE) }
}

// Stored ;; keep the original text so it come back in result
func Stored(on bool) FieldOption {
	return func(f *luxirpb.FieldDef) { f.Stored = ptr(on) }
}

// WithAnalyzer ;; how text is split and normalized, ex WithAnalyzer("unicode_word", "nfkc_cf", "fold")
func WithAnalyzer(tokenizer string, filters ...string) FieldOption {
	return func(f *luxirpb.FieldDef) {
		a := &luxirpb.AnalyzerDef{Tokenizer: &luxirpb.AnalyzerComponent{Name: tokenizer}}
		for _, name := range filters {
			a.Filters = append(a.Filters, &luxirpb.AnalyzerComponent{Name: name})
		}
		f.Analyzer = a
	}
}

// WithParent ;; get settings from a parent field
func WithParent(name string) FieldOption {
	return func(f *luxirpb.FieldDef) { f.Parent = name }
}

// Schema ;; builder for a collection's fields, pass Build() to CreateCollection
//
// ;; a custom schema is added on top of Luxir's defaults, so title_t, year_i, etc. still work
type Schema struct {
	def  *luxirpb.SchemaDef
	errs []error
}

// NewSchema ;; empty Schema
func NewSchema() *Schema {
	return &Schema{def: &luxirpb.SchemaDef{
		Fields:    map[string]*luxirpb.FieldDef{},
		Templates: map[string]*luxirpb.FieldDef{},
	}}
}

// Text ;; analyzed text field
func (s *Schema) Text(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_TEXT, opts)
}

// String ;; unanalyzed string field
func (s *Schema) String(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_STRING, opts)
}

// Int ;; int64 field
func (s *Schema) Int(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_INT, opts)
}

// Float ;; float32 field
func (s *Schema) Float(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_FLOAT, opts)
}

// Double ;; float64 field
func (s *Schema) Double(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_DOUBLE, opts)
}

// Date ;; timestamp field
func (s *Schema) Date(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_DATE, opts)
}

// Geo ;; latitude/longitude field
func (s *Schema) Geo(name string, opts ...FieldOption) *Schema {
	return s.field(name, luxirpb.FieldDef_GEO_POINT, opts)
}

// Vector ;; dense float vector, metric is required because without one it's storage only (no kNN)
func (s *Schema) Vector(name string, dims int32, metric luxirpb.VectorMetric, opts ...FieldOption) *Schema {
	if dims <= 0 {
		s.errs = append(s.errs, &SchemaError{Name: name, Msg: "vector dims must be positive"})
	}
	if metric == luxirpb.VectorMetric_NONE {
		s.errs = append(s.errs, &SchemaError{Name: name, Msg: "vector metric is required (L2, IP, COSINE)"})
	}
	return s.field(name, luxirpb.FieldDef_VECTOR, append(opts[:len(opts):len(opts)], func(f *luxirpb.FieldDef) {
		f.Dims = ptr(dims)
		f.Metric = ptr(metric)
	}))
}

// Template ;; fields ending in the suffix get these settings, ex Template("_tag", STRING, Multi()) -> genre_tag
func (s *Schema) Template(suffix string, class luxirpb.FieldDef_FieldClass, opts ...FieldOption) *Schema {
	if len(suffix) < 2 || suffix[0] != '_' {
		s.errs = append(s.errs, &SchemaError{Name: suffix, Msg: `template name must start with "_"`})
		return s
	}
	if _, dup := s.def.Templates[suffix]; dup {
		s.errs = append(s.errs, &SchemaError{Name: suffix, Msg: "defined twice"})
		return s
	}
	f := &luxirpb.FieldDef{Type: &class}
	for _, o := range opts {
		if o != nil {
			o(f)
		}
	}
	s.def.Templates[suffix] = f
	return s
}

// field ;; set the schema based on helper type
func (s *Schema) field(name string, class luxirpb.FieldDef_FieldClass, opts []FieldOption) *Schema {
	switch name {
	case "":
		s.errs = append(s.errs, &SchemaError{Msg: "field name is required"})
		return s
	case "id", "_version_":
		s.errs = append(s.errs, &SchemaError{Name: name, Msg: "reserved field"})
		return s
	}
	if _, dup := s.def.Fields[name]; dup {
		s.errs = append(s.errs, &SchemaError{Name: name, Msg: "defined twice"})
		return s
	}
	f := &luxirpb.FieldDef{Type: &class}
	for _, o := range opts {
		if o != nil {
			o(f)
		}
	}
	s.def.Fields[name] = f
	return s
}

// Build ;; the protobuf schema, or *SchemaError(s) for everything wrong with it (joined)
func (s *Schema) Build() (*luxirpb.SchemaDef, error) {
	if len(s.errs) > 0 {
		return nil, errors.Join(s.errs...)
	}
	return s.def, nil
}

// MustBuild ;; Build that panics
func (s *Schema) MustBuild() *luxirpb.SchemaDef {
	def, err := s.Build()
	if err != nil {
		panic(err)
	}
	return def
}

// ptr ;; generic ptr
func ptr[T any](v T) *T { return &v }
