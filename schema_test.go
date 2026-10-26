package luxir

import (
	"errors"
	"strings"
	"testing"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func buildSchema(t *testing.T, s *Schema) *luxirpb.SchemaDef {
	t.Helper()

	def, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return def
}

func schemaErrors(t *testing.T, err error) []*SchemaError {
	t.Helper()

	if err == nil {
		t.Fatal("err = nil, want *SchemaError")
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("err = %T, want joined errors", err)
	}

	var out []*SchemaError
	for _, e := range joined.Unwrap() {
		var se *SchemaError
		if !errors.As(e, &se) {
			t.Fatalf("joined err = %v (%T), want *SchemaError", e, e)
		}
		out = append(out, se)
	}
	return out
}

func TestSchema_FieldTypes(t *testing.T) {
	def := buildSchema(t, NewSchema().
		Text("a").
		String("b").
		Int("c").
		Float("d").
		Double("e").
		Date("f").
		Geo("g"))

	want := map[string]luxirpb.FieldDef_FieldClass{
		"a": luxirpb.FieldDef_TEXT,
		"b": luxirpb.FieldDef_STRING,
		"c": luxirpb.FieldDef_INT,
		"d": luxirpb.FieldDef_FLOAT,
		"e": luxirpb.FieldDef_DOUBLE,
		"f": luxirpb.FieldDef_DATE,
		"g": luxirpb.FieldDef_GEO_POINT,
	}
	if len(def.GetFields()) != len(want) {
		t.Errorf("got %d fields, want %d", len(def.GetFields()), len(want))
	}
	for name, class := range want {
		f := def.GetFields()[name]
		if f == nil {
			t.Errorf("field %q missing", name)
			continue
		}
		if f.GetType() != class {
			t.Errorf("field %q type = %s, want %s", name, f.GetType(), class)
		}
	}
}

func TestSchema_Options(t *testing.T) {
	tests := []struct {
		name  string
		opt   FieldOption
		check func(*luxirpb.FieldDef) bool
	}{
		{"Column", Column(), func(f *luxirpb.FieldDef) bool { return f.Column != nil && *f.Column && f.Multi == nil }},
		{"Multi", Multi(), func(f *luxirpb.FieldDef) bool { return f.Multi != nil && *f.Multi && f.Column == nil }},
		{"Range", Range(), func(f *luxirpb.FieldDef) bool {
			return f.GetIndex() == luxirpb.FieldDef_RANGE && f.Index != nil && f.Column != nil && *f.Column
		}},
		{"Unindexed", Unindexed(), func(f *luxirpb.FieldDef) bool {
			return f.Index != nil && f.GetIndex() == luxirpb.FieldDef_NONE
		}},
		{"Stored true", Stored(true), func(f *luxirpb.FieldDef) bool { return f.Stored != nil && *f.Stored }},
		{"Stored false", Stored(false), func(f *luxirpb.FieldDef) bool { return f.Stored != nil && !*f.Stored }},
		{"WithParent", WithParent("_s"), func(f *luxirpb.FieldDef) bool { return f.GetParent() == "_s" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := buildSchema(t, NewSchema().Text("x", tt.opt)).GetFields()["x"]

			if !tt.check(f) {
				t.Errorf("field = %v, option %s not applied as expected", f, tt.name)
			}
		})
	}
}

func TestSchema_UnsetOptionsStayUnset(t *testing.T) {
	f := buildSchema(t, NewSchema().Int("x")).GetFields()["x"]

	if f.Index != nil || f.Column != nil || f.Multi != nil || f.Stored != nil || f.Dims != nil || f.Metric != nil {
		t.Errorf("field = %v, want only the type set so Luxir can inherit defaults", f)
	}
}

func TestSchema_OptionsCombine(t *testing.T) {
	f := buildSchema(t, NewSchema().Int("tags", Multi(), Range())).GetFields()["tags"]

	if f.Multi == nil || !*f.Multi || f.Column == nil || !*f.Column || f.GetIndex() != luxirpb.FieldDef_RANGE {
		t.Errorf("field = %v, want multi + range + column", f)
	}
}

func TestSchema_WithAnalyzer(t *testing.T) {
	f := buildSchema(t, NewSchema().Text("title", WithAnalyzer("unicode_word", "nfkc_cf", "fold", "kstem"))).GetFields()["title"]

	a := f.GetAnalyzer()
	if a.GetTokenizer().GetName() != "unicode_word" {
		t.Errorf("tokenizer = %q, want unicode_word", a.GetTokenizer().GetName())
	}

	var got []string
	for _, c := range a.GetFilters() {
		got = append(got, c.GetName())
	}
	if want := "nfkc_cf,fold,kstem"; strings.Join(got, ",") != want {
		t.Errorf("filters = %v, want %s in order", got, want)
	}
}

func TestSchema_WithAnalyzer_NoFilters(t *testing.T) {
	f := buildSchema(t, NewSchema().Text("title", WithAnalyzer("keyword"))).GetFields()["title"]

	if f.GetAnalyzer().GetTokenizer().GetName() != "keyword" {
		t.Errorf("tokenizer = %q, want keyword", f.GetAnalyzer().GetTokenizer().GetName())
	}
	if n := len(f.GetAnalyzer().GetFilters()); n != 0 {
		t.Errorf("got %d filters, want 0", n)
	}
}

func TestSchema_Vector(t *testing.T) {
	f := buildSchema(t, NewSchema().Vector("embedding", 384, luxirpb.VectorMetric_COSINE, Column())).GetFields()["embedding"]

	if f.GetType() != luxirpb.FieldDef_VECTOR {
		t.Errorf("type = %s, want VECTOR", f.GetType())
	}
	if f.Dims == nil || *f.Dims != 384 {
		t.Errorf("dims = %v, want 384", f.Dims)
	}
	if f.Metric == nil || *f.Metric != luxirpb.VectorMetric_COSINE {
		t.Errorf("metric = %v, want COSINE", f.Metric)
	}
	if f.Column == nil || !*f.Column {
		t.Error("Column option was dropped")
	}
}

func TestSchema_Vector_DoesNotTouchCallersOptions(t *testing.T) {
	opts := make([]FieldOption, 1, 4)
	opts[0] = Column()
	spare := opts[:2]

	NewSchema().Vector("a", 3, luxirpb.VectorMetric_L2, opts...)

	if spare[1] != nil {
		t.Error("Vector wrote into the caller's options slice")
	}
}

func TestSchema_Template(t *testing.T) {
	def := buildSchema(t, NewSchema().Template("_tag", luxirpb.FieldDef_STRING, Multi()))

	if len(def.GetFields()) != 0 {
		t.Errorf("got %d fields, want 0", len(def.GetFields()))
	}
	f := def.GetTemplates()["_tag"]
	if f == nil {
		t.Fatal(`template "_tag" missing`)
	}
	if f.GetType() != luxirpb.FieldDef_STRING || f.Multi == nil || !*f.Multi {
		t.Errorf("template = %v, want multi STRING", f)
	}
}

func TestSchema_Chaining(t *testing.T) {
	s := NewSchema()

	if s.Text("a") != s || s.Vector("v", 2, luxirpb.VectorMetric_IP) != s || s.Template("_x", luxirpb.FieldDef_INT) != s {
		t.Error("builder methods must return the same *Schema to chain")
	}
	if s.Text("") != s || s.Text("id") != s {
		t.Error("builder methods must keep chaining after an error")
	}
}

func TestSchema_Empty(t *testing.T) {
	def := buildSchema(t, NewSchema())

	if len(def.GetFields()) != 0 || len(def.GetTemplates()) != 0 {
		t.Errorf("def = %v, want empty", def)
	}
}

func TestSchema_Errors(t *testing.T) {
	tests := []struct {
		name     string
		schema   *Schema
		wantName string
		wantMsg  string
	}{
		{"no name", NewSchema().Text(""), "", "field name is required"},
		{"id", NewSchema().Int("id"), "id", "reserved field"},
		{"version", NewSchema().Int("_version_"), "_version_", "reserved field"},
		{"duplicate", NewSchema().Text("a").Int("a"), "a", "defined twice"},
		{"vector dims zero", NewSchema().Vector("v", 0, luxirpb.VectorMetric_L2), "v", "dims must be positive"},
		{"vector dims negative", NewSchema().Vector("v", -3, luxirpb.VectorMetric_L2), "v", "dims must be positive"},
		{"vector no metric", NewSchema().Vector("v", 4, luxirpb.VectorMetric_NONE), "v", "metric is required"},
		{"template no underscore", NewSchema().Template("tag", luxirpb.FieldDef_STRING), "tag", `must start with "_"`},
		{"template just underscore", NewSchema().Template("_", luxirpb.FieldDef_STRING), "_", `must start with "_"`},
		{"template empty", NewSchema().Template("", luxirpb.FieldDef_STRING), "", `must start with "_"`},
		{"template duplicate", NewSchema().Template("_t2", luxirpb.FieldDef_TEXT).Template("_t2", luxirpb.FieldDef_INT), "_t2", "defined twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.schema.Build()

			if def != nil {
				t.Errorf("def = %v, want nil on error", def)
			}
			errs := schemaErrors(t, err)
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(errs), err)
			}
			if errs[0].Name != tt.wantName || !strings.Contains(errs[0].Msg, tt.wantMsg) {
				t.Errorf("err = {%q, %q}, want {%q, ...%q...}", errs[0].Name, errs[0].Msg, tt.wantName, tt.wantMsg)
			}
		})
	}
}

func TestSchema_ErrorsAreAllReported(t *testing.T) {
	_, err := NewSchema().
		Text("").
		Int("id").
		Text("a").
		Text("a").
		Vector("v", 0, luxirpb.VectorMetric_NONE).
		Template("tag", luxirpb.FieldDef_STRING).
		Build()

	if got := len(schemaErrors(t, err)); got != 6 {
		t.Errorf("got %d errors, want 6: %v", got, err)
	}
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Error("errors.As(err, *SchemaError) = false")
	}
}

func TestSchema_BuildIsRepeatable(t *testing.T) {
	s := NewSchema().Text("a")

	first, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("Build returned different defs for the same Schema")
	}
}

func TestSchema_MustBuild(t *testing.T) {
	if def := NewSchema().Text("a").MustBuild(); def.GetFields()["a"] == nil {
		t.Errorf("def = %v, want field a", def)
	}

	defer func() {
		if recover() == nil {
			t.Error("MustBuild did not panic on a bad schema")
		}
	}()
	NewSchema().Int("id").MustBuild()
}

func TestSchemaError_Error(t *testing.T) {
	tests := []struct {
		err  *SchemaError
		want string
	}{
		{&SchemaError{Msg: "field name is required"}, "[go-luxir] schema: field name is required"},
		{&SchemaError{Name: "id", Msg: "reserved field"}, `[go-luxir] schema "id": reserved field`},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

func TestPtr(t *testing.T) {
	a, b := ptr(true), ptr(true)

	if !*a || a == b {
		t.Error("ptr must return a pointer to a fresh copy of the value")
	}
}

func TestCreateCollection_WithSchema(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	def := buildSchema(t, NewSchema().
		Text("title", WithAnalyzer("unicode_word", "nfkc_cf", "fold")).
		String("author", Column()).
		Int("year", Range()).
		Vector("embedding", 4, luxirpb.VectorMetric_COSINE).
		Template("_tag", luxirpb.FieldDef_STRING, Multi()))

	if err := l.CreateCollection(timeout(t), name, def); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	resp, err := l.admin.GetSchema(timeout(t), &luxirpb.SchemaRequest{Collection: name})
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	fields := resp.GetSchema().GetFields()

	if got := fields["title"].GetAnalyzer().GetTokenizer().GetName(); got != "unicode_word" {
		t.Errorf("title tokenizer = %q, want unicode_word", got)
	}
	if f := fields["author"]; f.GetType() != luxirpb.FieldDef_STRING || f.GetColumn() != true {
		t.Errorf("author = %v, want STRING in a column", f)
	}
	if f := fields["year"]; f.GetType() != luxirpb.FieldDef_INT || f.GetIndex() != luxirpb.FieldDef_RANGE || f.GetColumn() != true {
		t.Errorf("year = %v, want INT with a RANGE index in a column", f)
	}
	if f := fields["embedding"]; f.GetType() != luxirpb.FieldDef_VECTOR || f.GetDims() != 4 || f.GetMetric() != luxirpb.VectorMetric_COSINE {
		t.Errorf("embedding = %v, want 4 dim COSINE VECTOR", f)
	}
	if f := resp.GetSchema().GetTemplates()["_tag"]; f.GetType() != luxirpb.FieldDef_STRING || f.GetMulti() != true {
		t.Errorf("_tag = %v, want multi STRING", f)
	}
}

func TestCreateCollection_WithSchema_KeepsDefaults(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	def := buildSchema(t, NewSchema().Text("title"))
	if err := l.CreateCollection(timeout(t), name, def); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	_, err := l.Index(timeout(t), name, []Document{
		{"id": "b1", "title": "Dune", "year_i": 1965, "blurb_t": "spice"},
	}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if got := liveDocs(t, l, name); got != 1 {
		t.Errorf("live docs = %d, want 1", got)
	}
}

func TestCreateCollection_WithSchema_Indexes(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	def := buildSchema(t, NewSchema().
		Text("title").
		Int("year", Range()).
		Vector("embedding", 4, luxirpb.VectorMetric_COSINE).
		Template("_tag", luxirpb.FieldDef_STRING, Multi()))
	if err := l.CreateCollection(timeout(t), name, def); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	_, err := l.Index(timeout(t), name, []Document{{
		"id":        "b1",
		"title":     "Dune",
		"year":      1965,
		"embedding": []float32{1, 0, 0, 0},
		"genre_tag": []string{"scifi", "classic"},
	}}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if got := liveDocs(t, l, name); got != 1 {
		t.Errorf("live docs = %d, want 1", got)
	}
}

func TestCreateCollection_WithSchema_ServerRejects(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	idType := luxirpb.FieldDef_INT
	def := &luxirpb.SchemaDef{Fields: map[string]*luxirpb.FieldDef{"id": {Type: &idType}}}

	err := l.CreateCollection(timeout(t), name, def)

	var ce *CollectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CollectionError", err, err)
	}
	if ce.Err == nil {
		t.Error("CollectionError.Err is nil, want Luxir's rejection")
	}
}
