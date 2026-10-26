package luxir

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func hitsRequest(top, req string) string {
	return `collection:"c" ` + req + ` ops:{key:"hits" value:{top_docs:{get_scores:true document_format:ROWS ` + top + `}}}`
}

func docList(ids ...string) *luxirpb.DocList {
	d := &luxirpb.DocList{RowCount: int32(len(ids))}
	for _, id := range ids {
		d.Docs = append(d.Docs, &luxirpb.Map{Fields: map[string]*luxirpb.Val{"id": {Kind: &luxirpb.Val_S{S: id}}}})
	}
	return d
}

func withFound(d *luxirpb.DocList, n int64) *luxirpb.DocList {
	d.Found = &n
	return d
}

func opsResp(more bool, ops map[string]*luxirpb.Val) *luxirpb.SearchResponse {
	return &luxirpb.SearchResponse{Ops: ops, More: more}
}

func hitsResp(more bool, d *luxirpb.DocList) *luxirpb.SearchResponse {
	return opsResp(more, map[string]*luxirpb.Val{opName: {Kind: &luxirpb.Val_Docs{Docs: d}}})
}

func TestSearchOptions_Request(t *testing.T) {
	tests := []struct {
		name string
		opts []SearchOption
		top  string
		req  string
	}{
		{"defaults", nil, `get_number:true`, ``},
		{"limit", []SearchOption{WithLimit(5)}, `get_number:true limit:5`, ``},
		{"limit zero", []SearchOption{WithLimit(0)}, `get_number:true limit:0`, ``},
		{"limit all", []SearchOption{WithLimit(-1)}, `get_number:true limit:-1`, ``},
		{"offset", []SearchOption{WithOffset(20)}, `get_number:true offset:20`, ``},
		{"fields", []SearchOption{WithFields("a", "b")}, `get_number:true fields:"a" fields:"b"`, ``},
		{"sort asc", []SearchOption{WithSort("year_i", false)}, `get_number:true sort:{expr:"year_i" dir:ASC}`, ``},
		{"sort desc", []SearchOption{WithSort("year_i", true)}, `get_number:true sort:{expr:"year_i" dir:DESC}`, ``},
		{"sort two keys", []SearchOption{WithSort("a", true), WithSort("b", false)},
			`get_number:true sort:{expr:"a" dir:DESC} sort:{expr:"b" dir:ASC}`, ``},
		{"sort vars", []SearchOption{WithSortVars("year_i - $y", false, map[string]any{"y": 1969})},
			`get_number:true sort:{expr:"year_i - $y" dir:ASC vars:{key:"y" value:{i:1969}}}`, ``},
		{"filter", []SearchOption{WithFilter(dune)},
			`get_number:true filter:{query:{` + dunePB + `}}`, ``},
		{"filter two", []SearchOption{WithFilter(dune), WithFilter(messiah)},
			`get_number:true filter:{query:{` + dunePB + `}} filter:{query:{` + messiaPB + `}}`, ``},
		{"filter except", []SearchOption{WithFilterExcept(dune, "a", "b")},
			`get_number:true filter:{query:{` + dunePB + `} except_ops:"a" except_ops:"b"}`, ``},
		{"freshness", []SearchOption{WithFreshness(1500 * time.Millisecond)}, `get_number:true`, `freshness_ms:1500`},
		{"time zone", []SearchOption{WithTimeZone("America/Denver")}, `get_number:true`, `time_zone:"America/Denver"`},
		{"profile", []SearchOption{WithProfile()}, `get_number:true`, `profile:true`},
		{"max parallel serial", []SearchOption{WithMaxParallel(1)}, `get_number:true`, `max_parallel:1`},
		{"max parallel all", []SearchOption{WithMaxParallel(-1)}, `get_number:true`, `max_parallel:-1`},
		{"max parallel auto", []SearchOption{WithMaxParallel(0)}, `get_number:true`, ``},
		{"batch size", []SearchOption{WithBatchSize(50)}, `get_number:true batch_size:50`, ``},
		{"request id", []SearchOption{WithRequestID("r1")}, `get_number:true`, `request_id:"r1"`},
		{"count off", []SearchOption{WithCount(false)}, ``, ``},
		{"count on", []SearchOption{WithCount(true)}, `get_number:true`, ``},
		{"all together", []SearchOption{WithLimit(3), WithOffset(1), WithTimeZone("UTC"), WithProfile(), WithRequestID("x"), WithBatchSize(7)},
			`get_number:true limit:3 offset:1 batch_size:7`, `time_zone:"UTC" profile:true request_id:"x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertProto(t, searchRequest(t, tt.opts...), wantRequest(t, hitsRequest(tt.top, tt.req)))
		})
	}
}

func TestSearchOptions_ScoresOff(t *testing.T) {
	want := wantRequest(t, `collection:"c" ops:{key:"hits" value:{top_docs:{get_number:true document_format:ROWS}}}`)

	assertProto(t, searchRequest(t, WithScores(false)), want)
}

func TestSearchOptions_ScopeErrors(t *testing.T) {
	l := &Luxir{}
	ctx := context.Background()
	dunes := []Source{FuseSource("a", dune)}

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{"rrf on search", func() error { _, err := l.Search(ctx, "c", nil, WithRRF(60)); return err }, "WithRRF does not apply to Search"},
		{"rrf negative", func() error { _, err := l.Fuse(ctx, "c", dunes, WithRRF(-1)); return err }, "WithRRF must not be negative"},
		{"offset negative", func() error { _, err := l.Search(ctx, "c", nil, WithOffset(-1)); return err }, "WithOffset must not be negative"},
		{"limit below all", func() error { _, err := l.Search(ctx, "c", nil, WithLimit(-2)); return err }, "WithLimit must be -1 or more"},
		{"freshness negative", func() error { _, err := l.Search(ctx, "c", nil, WithFreshness(-time.Second)); return err }, "WithFreshness must not be negative"},
		{"max parallel high", func() error { _, err := l.Search(ctx, "c", nil, WithMaxParallel(2)); return err }, "WithMaxParallel must be"},
		{"max parallel low", func() error { _, err := l.Search(ctx, "c", nil, WithMaxParallel(-2)); return err }, "WithMaxParallel must be"},
		{"sort vars bad", func() error {
			_, err := l.Search(ctx, "c", nil, WithSortVars("x", false, map[string]any{"v": struct{}{}}))
			return err
		}, "var"},
		{"op twice", func() error {
			_, err := l.Search(ctx, "c", nil, WithOp("a", Avg("x")), WithOp("a", Sum("x")))
			return err
		}, "defined twice"},

		{"fuse sort", func() error { _, err := l.Fuse(ctx, "c", dunes, WithSort("x", false)); return err }, "WithSort does not apply to Fuse"},
		{"fuse sort vars", func() error { _, err := l.Fuse(ctx, "c", dunes, WithSortVars("x", false, nil)); return err }, "WithSortVars does not apply to Fuse"},
		{"fuse filter except", func() error { _, err := l.Fuse(ctx, "c", dunes, WithFilterExcept(dune)); return err }, "WithFilterExcept does not apply to Fuse"},
		{"source freshness", func() error {
			_, err := l.Fuse(ctx, "c", []Source{FuseSource("a", dune, WithFreshness(time.Second))})
			return err
		}, "WithFreshness does not apply to FuseSource"},
		{"source offset", func() error {
			_, err := l.Fuse(ctx, "c", []Source{FuseSource("a", dune, WithOffset(1))})
			return err
		}, "WithOffset does not apply to FuseSource"},
		{"source fields", func() error {
			_, err := l.Fuse(ctx, "c", []Source{FuseSource("a", dune, WithFields("x"))})
			return err
		}, "WithFields does not apply to FuseSource"},
		{"source op", func() error {
			_, err := l.Fuse(ctx, "c", []Source{FuseSource("a", dune, WithOp("x", Avg("y")))})
			return err
		}, "WithOp does not apply to FuseSource"},

		{"do limit", func() error { _, err := l.Do(ctx, "c", map[string]Op{"a": Avg("x")}, WithLimit(1)); return err }, "WithLimit does not apply to Do"},
		{"do offset", func() error { _, err := l.Do(ctx, "c", map[string]Op{"a": Avg("x")}, WithOffset(1)); return err }, "WithOffset does not apply to Do"},
		{"do filter", func() error { _, err := l.Do(ctx, "c", map[string]Op{"a": Avg("x")}, WithFilter(dune)); return err }, "WithFilter does not apply to Do"},
		{"do op", func() error {
			_, err := l.Do(ctx, "c", map[string]Op{"a": Avg("x")}, WithOp("b", Avg("x")))
			return err
		}, "WithOp does not apply to Do"},

		{"docs freshness", func() error {
			_, err := l.Search(ctx, "c", nil, WithOp("d", FieldFacet("a", SubOp("x", Docs(nil, WithFreshness(time.Second))))))
			return err
		}, "WithFreshness does not apply to Docs"},
		{"docs profile", func() error {
			_, err := l.Do(ctx, "c", map[string]Op{"d": Docs(nil, WithProfile())})
			return err
		}, "WithProfile does not apply to Docs"},
		{"docs rrf", func() error {
			_, err := l.Do(ctx, "c", map[string]Op{"d": Docs(nil, WithRRF(1))})
			return err
		}, "WithRRF does not apply to Docs"},
		{"fusion op freshness", func() error {
			_, err := l.Do(ctx, "c", map[string]Op{"f": Fusion(dunes, WithFreshness(time.Second))})
			return err
		}, "WithFreshness does not apply to Fusion"},
		{"fusion op sort", func() error {
			_, err := l.Do(ctx, "c", map[string]Op{"f": Fusion(dunes, WithSort("x", false))})
			return err
		}, "WithSort does not apply to Fusion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()

			var se *SearchError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v (%T), want *SearchError", err, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestSearchOptions_UncountedPages(t *testing.T) {
	tests := []struct {
		name    string
		opts    []SearchOption
		ids     []string
		hasMore bool
	}{
		{"full page", []SearchOption{WithLimit(2)}, []string{"a", "b"}, true},
		{"short page", []SearchOption{WithLimit(3)}, []string{"a", "b"}, false},
		{"default limit full", nil, []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}, true},
		{"default limit short", nil, []string{"a"}, false},
		{"all", []SearchOption{WithLimit(-1)}, []string{"a", "b"}, false},
		{"limit zero", []SearchOption{WithLimit(0)}, nil, false},
		{"no hits", []SearchOption{WithLimit(2)}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, _ := fakeLuxir(t, fakeReply(hitsResp(false, docList(tt.ids...))))

			r, err := l.Search(timeout(t), "c", nil, append(tt.opts, WithCount(false))...)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if r.Found != 0 {
				t.Errorf("Found = %d, want 0 when not counted", r.Found)
			}
			if got := r.HasMore(); got != tt.hasMore {
				t.Errorf("HasMore() = %v, want %v", got, tt.hasMore)
			}
		})
	}
}

func TestSearchOptions_CountedPagesUnchanged(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(hitsResp(false, withFound(docList("a", "b"), 5))))

	r, err := l.Search(timeout(t), "c", nil, WithLimit(2), WithOffset(2))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 5 || r.Offset != 2 || !r.HasMore() || r.NextOffset() != 4 {
		t.Errorf("found = %d, offset = %d, hasMore = %v, next = %d", r.Found, r.Offset, r.HasMore(), r.NextOffset())
	}
}

func TestSearchOptions_NoHitsOpInResponse(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))

	r, err := l.Search(timeout(t), "c", nil, WithOffset(3))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Hits) != 0 || r.Offset != 3 || r.HasMore() {
		t.Errorf("result = %+v, want an empty page at offset 3", r)
	}
}

func TestSearchOptions_WarningsAndProfile(t *testing.T) {
	final := hitsResp(false, withFound(docList("a"), 1))
	final.Warnings = []*luxirpb.Warning{{Code: "fuzzy_clamped", Message: "m"}}
	final.Profile = &luxirpb.ExecutionProfile{Ops: []*luxirpb.ExecutionProfileOp{{Name: "x"}}}
	l, _ := fakeLuxir(t, fakeReply(final))

	r, err := l.Search(timeout(t), "c", nil, WithProfile())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].GetCode() != "fuzzy_clamped" {
		t.Errorf("Warnings = %v", r.Warnings)
	}
	if r.Profile.GetOps()[0].GetName() != "x" {
		t.Errorf("Profile = %v", r.Profile)
	}
}

func TestSearchOptions_LiveTimeZone(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)
	since := InRange("pub", Gte("1969-07-01"))

	utc, err := l.Search(timeout(t), coll, since)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	denver, err := l.Search(timeout(t), coll, since, WithTimeZone("America/Denver"))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got := sortedIDs(utc); !reflect.DeepEqual(got, []string{"b2", "b3"}) {
		t.Errorf("utc ids = %v, want [b2 b3]", got)
	}
	if got := sortedIDs(denver); !reflect.DeepEqual(got, []string{"b3"}) {
		t.Errorf("denver ids = %v, want [b3]: local midnight is after b2's UTC midnight", got)
	}
}

func TestSearchOptions_LiveSortVars(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Search(timeout(t), coll, nil, WithSortVars("(year_i - $y) * (year_i - $y)", false, map[string]any{"y": 1969}))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"b2", "b1", "b3"}) {
		t.Errorf("ids = %v, want closest to 1969 first [b2 b1 b3]", got)
	}
}

func TestSearchOptions_LiveCountAndScores(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Search(timeout(t), coll, dune, WithCount(false), WithScores(false), WithLimit(1))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 0 || len(r.Hits) != 1 || r.Hits[0].Score != 0 || !r.HasMore() {
		t.Errorf("found = %d, hits = %d, hasMore = %v, score = %v", r.Found, len(r.Hits), r.HasMore(), r.Hits[0].Score)
	}
}

func TestSearchOptions_LiveProfileAndBatches(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	docs := make([]Document, 60)
	for i := range docs {
		docs[i] = Document{"id": string(rune('A'+i/26)) + string(rune('a'+i%26)), "n_i": i, "g_s": "x"}
	}
	if _, err := l.Index(timeout(t), coll, docs, WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	r, err := l.Search(timeout(t), coll, nil, WithLimit(-1), WithSort("n_i", false), WithBatchSize(7), WithProfile(),
		WithOp("g", FieldFacet("g_s")))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 60 || len(r.Hits) != 60 {
		t.Fatalf("found = %d, hits = %d, want 60 and 60", r.Found, len(r.Hits))
	}
	for i, h := range r.Hits {
		if h.Doc["n_i"] != int64(i) {
			t.Fatalf("hit %d has n_i = %v, small batches out of order or duplicated", i, h.Doc["n_i"])
		}
	}
	if r.Profile == nil {
		t.Error("Profile = nil, want it with WithProfile")
	}
	if f := r.Ops.Facet("g"); f == nil || len(f.Buckets) != 1 || f.Buckets[0].Count != 60 {
		t.Errorf("facet g = %+v, want one bucket of 60", f)
	}
}

func TestSearchOptions_LiveRequestID(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	if _, err := l.Search(timeout(t), coll, nil, WithRequestID("r-1"), WithMaxParallel(1), WithFreshness(0)); err != nil {
		t.Fatalf("Search: %v", err)
	}
}
