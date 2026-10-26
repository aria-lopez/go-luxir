package luxir

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func fuseRequest(t *testing.T, sources []Source, opts ...SearchOption) *luxirpb.SearchRequest {
	t.Helper()
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	if _, err := l.Fuse(timeout(t), "c", sources, opts...); err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	return f.last(t)
}

func fusionText(body string) string {
	return `collection:"c" ops:{key:"hits" value:{fusion:{` + body + `}}}`
}

func TestFuse_Request(t *testing.T) {
	tests := []struct {
		name    string
		sources []Source
		opts    []SearchOption
		want    string
	}{
		{"one source", []Source{FuseSource("a", dune)}, nil,
			`sources:{key:"a" value:{query:{` + dunePB + `}}} get_number:true get_scores:true document_format:ROWS rrf:{}`},
		{"no query", []Source{FuseSource("a", nil)}, nil,
			`sources:{key:"a" value:{}} get_number:true get_scores:true document_format:ROWS rrf:{}`},
		{"two sources", []Source{FuseSource("a", dune), FuseSource("b", messiah)}, nil,
			`sources:{key:"a" value:{query:{` + dunePB + `}}} sources:{key:"b" value:{query:{` + messiaPB + `}}} get_number:true get_scores:true document_format:ROWS rrf:{}`},
		{"source options", []Source{FuseSource("a", dune, WithLimit(50), WithFilter(messiah), WithSort("year_i", true))}, nil,
			`sources:{key:"a" value:{query:{` + dunePB + `} limit:50 filter:{query:{` + messiaPB + `}} sort:{expr:"year_i" dir:DESC}}} get_number:true get_scores:true document_format:ROWS rrf:{}`},
		{"rrf k", []Source{FuseSource("a", dune)}, []SearchOption{WithRRF(10)},
			`sources:{key:"a" value:{query:{` + dunePB + `}}} get_number:true get_scores:true document_format:ROWS rrf:{k:10}`},
		{"rrf zero", []Source{FuseSource("a", dune)}, []SearchOption{WithRRF(0)},
			`sources:{key:"a" value:{query:{` + dunePB + `}}} get_number:true get_scores:true document_format:ROWS rrf:{k:0}`},
		{"page", []Source{FuseSource("a", dune)}, []SearchOption{WithLimit(5), WithOffset(10), WithFields("id")},
			`sources:{key:"a" value:{query:{` + dunePB + `}}} limit:5 offset:10 fields:"id" get_number:true get_scores:true document_format:ROWS rrf:{}`},
		{"shared filter", []Source{FuseSource("a", dune)}, []SearchOption{WithFilter(messiah)},
			`sources:{key:"a" value:{query:{` + dunePB + `}}} filter:{query:{` + messiaPB + `}} get_number:true get_scores:true document_format:ROWS rrf:{}`},
		{"no count no scores", []Source{FuseSource("a", dune)}, []SearchOption{WithCount(false), WithScores(false), WithBatchSize(7)},
			`sources:{key:"a" value:{query:{` + dunePB + `}}} batch_size:7 document_format:ROWS rrf:{}`},
		{"ops over the union", []Source{FuseSource("a", dune)}, []SearchOption{WithOp("avg", Avg("price_d"))},
			`sources:{key:"a" value:{query:{` + dunePB + `}}} get_number:true get_scores:true document_format:ROWS ops:{key:"avg" value:{expr_op:{expr:"avg(price_d)"}}} rrf:{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertProto(t, fuseRequest(t, tt.sources, tt.opts...), wantRequest(t, fusionText(tt.want)))
		})
	}
}

func TestFuse_RequestLevel(t *testing.T) {
	req := fuseRequest(t, []Source{FuseSource("a", dune)}, WithRequestID("r1"), WithTimeZone("UTC"), WithMaxParallel(1))
	if req.GetRequestId() != "r1" || req.GetTimeZone() != "UTC" || req.GetMaxParallel() != 1 {
		t.Errorf("request = %v, want the request level options on the request", req)
	}
}

func TestFuse_Errors(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	tests := []struct {
		name    string
		sources []Source
		want    string
	}{
		{"no sources", nil, "at least one source"},
		{"empty name", []Source{FuseSource("", dune)}, "needs a unique name"},
		{"duplicate name", []Source{FuseSource("a", dune), FuseSource("a", messiah)}, "needs a unique name"},
		{"bad query", []Source{FuseSource("a", Knn("e", nil, 1))}, `source "a"`},
		{"bad filter", []Source{FuseSource("a", dune, WithFilter(Knn("e", nil, 1)))}, `source "a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := l.Fuse(timeout(t), "c", tt.sources)
			var se *SearchError
			if !errors.As(err, &se) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v (%T), want *SearchError containing %q", err, err, tt.want)
			}
		})
	}
	if f.calls() != 0 {
		t.Errorf("server saw %d requests, want invalid calls never sent", f.calls())
	}
}

func TestFuse_ResultPaging(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(hitsResp(false, withFound(docList("b1", "b2"), 7))))

	r, err := l.Fuse(timeout(t), "c", []Source{FuseSource("a", dune)}, WithLimit(2), WithOffset(4))
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if r.Found != 7 || r.Offset != 4 || !r.HasMore() {
		t.Errorf("found = %d offset = %d more = %v, want 7, 4, true", r.Found, r.Offset, r.HasMore())
	}
}

func TestFuse_ResultOps(t *testing.T) {
	d := withFound(docList("b1"), 1)
	d.Ops = map[string]*luxirpb.Val{"avg": {Kind: &luxirpb.Val_D{D: 3.5}}}
	l, _ := fakeLuxir(t, fakeReply(hitsResp(false, d)))

	r, err := l.Fuse(timeout(t), "c", []Source{FuseSource("a", dune)}, WithOp("avg", Avg("price_d")))
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if v, ok := r.Ops.Float("avg"); !ok || v != 3.5 {
		t.Errorf("avg = %v, %v", v, ok)
	}
}

func TestDo_Request(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))

	_, err := l.Do(timeout(t), "c", map[string]Op{
		"b":    Avg("price_d"),
		"a":    FieldFacet("author_s"),
		"docs": Docs(dune, WithLimit(2)),
		"all":  InDomain(Sum("year_i"), WithDomainQuery(All())),
		"f":    Fusion([]Source{FuseSource("x", dune)}, WithRRF(5), WithLimit(3)),
	}, WithRequestID("r"), WithProfile())
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	want := wantRequest(t, `collection:"c" request_id:"r" profile:true
		ops:{key:"a" value:{field_facet:{field:"author_s"}}}
		ops:{key:"all" value:{expr_op:{expr:"sum(year_i)"} domain:{query:{all:true}}}}
		ops:{key:"b" value:{expr_op:{expr:"avg(price_d)"}}}
		ops:{key:"docs" value:{top_docs:{query:{`+dunePB+`} limit:2 get_number:true get_scores:true document_format:ROWS}}}
		ops:{key:"f" value:{fusion:{sources:{key:"x" value:{query:{`+dunePB+`}}} limit:3 get_number:true get_scores:true document_format:ROWS rrf:{k:5}}}}`)
	assertProto(t, f.last(t), want)
}

func TestDo_Errors(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	tests := []struct {
		name string
		ops  map[string]Op
		want string
	}{
		{"nil map", nil, "Do needs at least one op"},
		{"empty map", map[string]Op{}, "Do needs at least one op"},
		{"nil op", map[string]Op{"a": nil}, `op "a"`},
		{"bad op", map[string]Op{"ok": Avg("x"), "bad": RangeFacet("y", 0, 1)}, `op "bad"`},
		{"bad docs query", map[string]Op{"d": Docs(Knn("e", nil, 1))}, "vector is empty"},
		{"empty fusion", map[string]Op{"f": Fusion(nil)}, "at least one source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := l.Do(timeout(t), "c", tt.ops)
			var se *SearchError
			if resp != nil || !errors.As(err, &se) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("resp = %v err = %v (%T), want nil and *SearchError containing %q", resp, err, err, tt.want)
			}
		})
	}
	if f.calls() != 0 {
		t.Errorf("server saw %d requests, want invalid calls never sent", f.calls())
	}
}

func TestDo_ErrorKeepsCause(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))

	_, err := l.Do(timeout(t), "c", map[string]Op{"a": RangeFacet("y", 0, 1)})

	var oe *OpError
	if !errors.As(err, &oe) || oe.Op != "a" {
		t.Errorf("err = %v, want an *OpError for \"a\" reachable with errors.As", err)
	}
}

func TestDo_Response(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(
		&luxirpb.SearchResponse{Ops: map[string]*luxirpb.Val{"avg": {Kind: &luxirpb.Val_D{D: 2}}}, More: false,
			Warnings: []*luxirpb.Warning{{Message: "w"}}},
	))

	r, err := l.Do(timeout(t), "c", map[string]Op{"avg": Avg("x")})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if v, _ := r.Ops.Float("avg"); v != 2 || len(r.Warnings) != 1 {
		t.Errorf("response = %+v", r)
	}
}

func TestDo_ServerError(t *testing.T) {
	l, _ := fakeLuxir(t, func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error) {
		return nil, errors.New("boom")
	})

	r, err := l.Do(timeout(t), "c", map[string]Op{"avg": Avg("x")})

	var se *SearchError
	if r != nil || !errors.As(err, &se) {
		t.Errorf("resp = %v err = %v, want nil and *SearchError", r, err)
	}
}

func TestFuseLive_RRF(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)
	score := func(k float64, ranks ...int) float64 {
		var s float64
		for _, r := range ranks {
			s += 1 / (k + float64(r))
		}
		return s
	}
	byYear := FuseSource("year", All(), WithSort("year_i", false))
	byPrice := FuseSource("price", All(), WithSort("price_d", true))

	tests := []struct {
		name string
		opts []SearchOption
		k    float64
		want map[string]float64
	}{
		{"default k", nil, 60, map[string]float64{"b1": score(60, 1, 2), "b3": score(60, 3, 1), "b2": score(60, 2, 3)}},
		{"k 1", []SearchOption{WithRRF(1)}, 1, map[string]float64{"b1": score(1, 1, 2), "b3": score(1, 3, 1), "b2": score(1, 2, 3)}},
		{"k 0 is the default", []SearchOption{WithRRF(0)}, 60, map[string]float64{"b1": score(60, 1, 2), "b3": score(60, 3, 1), "b2": score(60, 2, 3)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := l.Fuse(timeout(t), coll, []Source{byYear, byPrice}, tt.opts...)
			if err != nil {
				t.Fatalf("Fuse: %v", err)
			}
			if !reflect.DeepEqual(ids(r), []string{"b1", "b3", "b2"}) || r.Found != 3 {
				t.Fatalf("ids = %v found = %d, want [b1 b3 b2] and 3", ids(r), r.Found)
			}
			for _, h := range r.Hits {
				if want := tt.want[h.Doc["id"].(string)]; math.Abs(float64(h.Score)-want) > 1e-4 {
					t.Errorf("%v score = %v, want %v", h.Doc["id"], h.Score, want)
				}
			}
		})
	}
}

func TestFuseLive_HybridTextAndVector(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Fuse(timeout(t), coll, []Source{
		FuseSource("text", Match("title_t", "neuromancer")),
		FuseSource("vec", Knn("emb", []float32{0, 1, 0}, 3)),
	})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if len(r.Hits) == 0 || ids(r)[0] != "b3" {
		t.Errorf("ids = %v, want b3 first: best in both lists", ids(r))
	}
}

func TestFuseLive_SingleSourceKeepsOrder(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Fuse(timeout(t), coll, []Source{FuseSource("p", All(), WithSort("price_d", false))})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if !reflect.DeepEqual(ids(r), []string{"b2", "b1", "b3"}) {
		t.Errorf("ids = %v, want the source's own order", ids(r))
	}
}

func TestFuseLive_Paging(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)
	srcs := []Source{FuseSource("year", All(), WithSort("year_i", false)), FuseSource("price", All(), WithSort("price_d", true))}

	p1, err := l.Fuse(timeout(t), coll, srcs, WithLimit(2))
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	p2, err := l.Fuse(timeout(t), coll, srcs, WithLimit(2), WithOffset(2))
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if !reflect.DeepEqual(ids(p1), []string{"b1", "b3"}) || !reflect.DeepEqual(ids(p2), []string{"b2"}) {
		t.Errorf("pages = %v, %v, want [b1 b3] then [b2]", ids(p1), ids(p2))
	}
	if !p1.HasMore() || p2.HasMore() || p2.Offset != 2 {
		t.Errorf("more = %v, %v, offset = %d", p1.HasMore(), p2.HasMore(), p2.Offset)
	}
}

func TestFuseLive_SourceLimitsArePools(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Fuse(timeout(t), coll, []Source{
		FuseSource("old", All(), WithSort("year_i", false), WithLimit(1)),
		FuseSource("pricey", All(), WithSort("price_d", true), WithLimit(1)),
	})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if got := sortedIDs(r); !reflect.DeepEqual(got, []string{"b1", "b3"}) || r.Found != 2 {
		t.Errorf("ids = %v found = %d, want only the two pool leaders", got, r.Found)
	}
}

func TestFuseLive_SourceFilter(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Fuse(timeout(t), coll, []Source{
		FuseSource("herbert", All(), WithFilter(Match("author_s", "Herbert"))),
		FuseSource("gibson", All(), WithFilter(Match("author_s", "Gibson"))),
	})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if got := sortedIDs(r); !reflect.DeepEqual(got, []string{"b1", "b2", "b3"}) {
		t.Errorf("ids = %v, want each source's own filter applied", got)
	}

	shared, err := l.Fuse(timeout(t), coll, []Source{FuseSource("a", All())}, WithFilter(Match("author_s", "Gibson")))
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if !reflect.DeepEqual(ids(shared), []string{"b3"}) {
		t.Errorf("ids = %v, want the shared filter applied to the merged list", ids(shared))
	}
}

func TestFuseLive_OpsOverUnion(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Fuse(timeout(t), coll, []Source{
		FuseSource("a", Match("author_s", "Gibson")),
		FuseSource("b", dune),
	}, WithLimit(0), WithOp("authors", FieldFacet("author_s")), WithOp("sum", Sum("year_i")))
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if r.Found != 3 || len(r.Hits) != 0 {
		t.Errorf("found = %d hits = %d", r.Found, len(r.Hits))
	}
	if got := bucketCounts(r.Ops.Facet("authors")); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("author counts = %v, want the union's", got)
	}
	if v, ok := r.Ops.Int("sum"); !ok || v != 1965+1969+1984 {
		t.Errorf("sum = %v, %v", v, ok)
	}
}

func TestFuseLive_Rejected(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	_, err := l.Fuse(timeout(t), coll, []Source{FuseSource("a", Knn("emb", []float32{1, 0}, 2))})

	var se *SearchError
	if !errors.As(err, &se) || se.RequestFailed() == nil {
		t.Errorf("err = %v (%T), want *SearchError carrying Luxir's rejection of the wrong sized vector", err, err)
	}
}

func TestDoLive_ManyOps(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Do(timeout(t), coll, map[string]Op{
		"authors": FieldFacet("author_s"),
		"avg":     Avg("price_d"),
		"dunes":   Docs(dune, WithFields("id"), WithSort("year_i", false)),
		"cheap":   InDomain(Docs(nil, WithFields("id")), WithDomainQuery(InRange("price_d", Lt(9)))),
		"gibson":  InDomain(Sum("year_i"), WithDomainQuery(Match("author_s", "Gibson"))),
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := bucketValues(r.Ops.Facet("authors")); !reflect.DeepEqual(got, []any{"Herbert", "Gibson"}) {
		t.Errorf("authors = %v, want the whole collection", got)
	}
	if v, _ := r.Ops.Float("avg"); v < 10.16 || v > 10.17 {
		t.Errorf("avg = %v", v)
	}
	if d := r.Ops.Docs("dunes"); d == nil || !reflect.DeepEqual(ids(d), []string{"b1", "b2"}) || d.Found != 2 {
		t.Errorf("dunes = %+v", d)
	}
	if d := r.Ops.Docs("cheap"); d == nil || !reflect.DeepEqual(ids(d), []string{"b2"}) {
		t.Errorf("cheap = %+v", d)
	}
	if v, _ := r.Ops.Int("gibson"); v != 1984 {
		t.Errorf("gibson = %v", v)
	}
}

func TestDoLive_FusionOp(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Do(timeout(t), coll, map[string]Op{
		"hybrid": Fusion([]Source{
			FuseSource("year", All(), WithSort("year_i", false)),
			FuseSource("price", All(), WithSort("price_d", true)),
		}, WithLimit(2), WithFields("id")),
		"all": Docs(nil, WithLimit(0)),
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	h := r.Ops.Docs("hybrid")
	if h == nil || !reflect.DeepEqual(ids(h), []string{"b1", "b3"}) || h.Found != 3 {
		t.Errorf("hybrid = %+v, want [b1 b3] of 3", h)
	}
	if a := r.Ops.Docs("all"); a == nil || a.Found != 3 || len(a.Hits) != 0 {
		t.Errorf("all = %+v, want a count only", a)
	}
}

func TestDoLive_OneBadOpFailsAll(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r, err := l.Do(timeout(t), coll, map[string]Op{
		"fine": Avg("price_d"),
		"bad":  Metric("sum(", nil),
	})

	var se *SearchError
	if r != nil || !errors.As(err, &se) {
		t.Errorf("resp = %v err = %v, want no partial response and a *SearchError", r, err)
	}
}

func TestDoLive_MissingCollection(t *testing.T) {
	l := getClient(t)

	_, err := l.Do(timeout(t), "no_such_collection_xyz", map[string]Op{"a": Avg("x")})

	var se *SearchError
	if !errors.As(err, &se) {
		t.Errorf("err = %v (%T), want *SearchError", err, err)
	}
}
