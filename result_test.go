package luxir

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func facetOf(t *testing.T, text string) *Facet {
	t.Helper()
	f, err := decodeFacet(parseText(t, text, &luxirpb.FacetResult{}))
	if err != nil {
		t.Fatalf("decodeFacet: %v", err)
	}
	return f
}

func TestDecodeFacet_BucketKinds(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Bucket
	}{
		{"strings", `bucket_ids:{col_s:{v:"a" v:"b"}} counts:2 counts:1`,
			[]Bucket{{Value: "a", Count: 2}, {Value: "b", Count: 1}}},
		{"ints", `bucket_ids:{col_i:{v:1965 v:1969}} counts:1 counts:1`,
			[]Bucket{{Value: int64(1965), Count: 1}, {Value: int64(1969), Count: 1}}},
		{"floats", `bucket_ids:{col_d:{v:1.5}} counts:3`, []Bucket{{Value: 1.5, Count: 3}}},
		{"float32", `bucket_ids:{col_f:{v:0.5}} counts:3`, []Bucket{{Value: 0.5, Count: 3}}},
		{"int ranges", `bucket_ids:{multi_i:{v:{v:1960 v:1970} v:{v:1970 v:1980}}} counts:2 counts:0`,
			[]Bucket{{Value: BucketRange{int64(1960), int64(1970)}, Count: 2}, {Value: BucketRange{int64(1970), int64(1980)}, Count: 0}}},
		{"double ranges", `bucket_ids:{multi_d:{v:{v:0.5 v:1.5}}} counts:1`,
			[]Bucket{{Value: BucketRange{0.5, 1.5}, Count: 1}}},
		{"float ranges widen", `bucket_ids:{multi_f:{v:{v:0.5 v:1.5}}} counts:1`,
			[]Bucket{{Value: BucketRange{0.5, 1.5}, Count: 1}}},
		{"not a pair stays a slice", `bucket_ids:{multi_i:{v:{v:1 v:2 v:3}}} counts:1`,
			[]Bucket{{Value: []int64{1, 2, 3}, Count: 1}}},
		{"no buckets", `bucket_ids:{col_s:{}}`, []Bucket{}},
		{"no column", ``, []Bucket{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := facetOf(t, tt.text).Buckets; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buckets = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDecodeFacet_Missing(t *testing.T) {
	if f := facetOf(t, `bucket_ids:{col_s:{v:"a"}} counts:1`); f.Missing != nil {
		t.Errorf("Missing = %d, want nil when not asked for", *f.Missing)
	}
	if f := facetOf(t, `bucket_ids:{col_s:{v:"a"}} counts:1 missing:0`); f.Missing == nil || *f.Missing != 0 {
		t.Errorf("Missing = %v, want a pointer to 0", f.Missing)
	}
	if f := facetOf(t, `bucket_ids:{col_s:{v:"a"}} counts:1 missing:4`); f.Missing == nil || *f.Missing != 4 {
		t.Errorf("Missing = %v, want a pointer to 4", f.Missing)
	}
}

func TestDecodeFacet_SubOps(t *testing.T) {
	f := facetOf(t, `
		bucket_ids:{col_s:{v:"a" v:"b" v:"c"}} counts:2 counts:1 counts:0
		ops:{key:"avg" value:{arr:{v:{d:9.25} v:{d:12} v:{null:NULL_VALUE}}}}
		ops:{key:"sum" value:{arr:{v:{i:3} v:{i:4} v:{null:NULL_VALUE}}}}
		ops:{key:"sumf" value:{arr:{v:{f:1.5} v:{f:2.5} v:{f:3.5}}}}
		ops:{key:"best" value:{arr:{
			v:{docs:{found:2 docs:{fields:{key:"id" value:{s:"b1"}}} row_count:1}}
			v:{docs:{found:1 docs:{fields:{key:"id" value:{s:"b3"}}} row_count:1}}
			v:{docs:{found:0}}}}}
		ops:{key:"inner" value:{arr:{
			v:{facet:{bucket_ids:{col_s:{v:"x"}} counts:2}}
			v:{facet:{bucket_ids:{col_s:{}}}}
			v:{facet:{bucket_ids:{col_s:{}}}}}}}`)

	a, b, c := f.Buckets[0], f.Buckets[1], f.Buckets[2]
	if v, ok := a.Ops.Float("avg"); !ok || v != 9.25 {
		t.Errorf("a avg = %v, %v", v, ok)
	}
	if _, ok := c.Ops.Float("avg"); ok {
		t.Error("c avg has a value, want none for an empty bucket")
	}
	if v, ok := b.Ops.Int("sum"); !ok || v != 4 {
		t.Errorf("b sum = %v, %v", v, ok)
	}
	if v, ok := a.Ops.Float("sumf"); !ok || v != 1.5 {
		t.Errorf("a sumf = %v, %v", v, ok)
	}
	if d := a.Ops.Docs("best"); d == nil || d.Found != 2 || ids(d)[0] != "b1" {
		t.Errorf("a best = %+v", d)
	}
	if d := c.Ops.Docs("best"); d == nil || d.Found != 0 || len(d.Hits) != 0 {
		t.Errorf("c best = %+v, want an empty page", d)
	}
	if in := a.Ops.Facet("inner"); in == nil || len(in.Buckets) != 1 || in.Buckets[0].Count != 2 {
		t.Errorf("a inner = %+v", in)
	}
	if in := c.Ops.Facet("inner"); in == nil || len(in.Buckets) != 0 {
		t.Errorf("c inner = %+v", in)
	}
}

func TestDecodeFacet_NoSubOpsMeansNilOps(t *testing.T) {
	f := facetOf(t, `bucket_ids:{col_s:{v:"a"}} counts:1`)

	if f.Buckets[0].Ops != nil {
		t.Errorf("Ops = %v, want nil", f.Buckets[0].Ops)
	}
}

func TestDecodeFacet_Errors(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"counts short", `bucket_ids:{col_s:{v:"a" v:"b"}} counts:1`, "2 buckets but 1 counts"},
		{"counts long", `bucket_ids:{col_s:{v:"a"}} counts:1 counts:2`, "1 buckets but 2 counts"},
		{"vector column", `bucket_ids:{col_vec:{}}`, "unsupported bucket column"},
		{"sub op short", `bucket_ids:{col_s:{v:"a" v:"b"}} counts:1 counts:1 ops:{key:"m" value:{arr:{v:{d:1}}}}`, `sub op "m" has 1 results for 2 buckets`},
		{"sub op not a list", `bucket_ids:{col_s:{v:"a"}} counts:1 ops:{key:"m" value:{d:1}}`, `sub op "m" has 0 results for 1 buckets`},
		{"sub op bad value", `bucket_ids:{col_s:{v:"a"}} counts:1 ops:{key:"m" value:{arr:{v:{arr:{v:{docs:{}}}}}}}`, "unsupported value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeFacet(parseText(t, tt.text, &luxirpb.FacetResult{}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestDecodeVal(t *testing.T) {
	tests := []struct {
		name string
		text string
		want any
	}{
		{"int", `i:7`, int64(7)},
		{"double", `d:1.5`, 1.5},
		{"float", `f:0.5`, 0.5},
		{"null", `null:NULL_VALUE`, nil},
		{"unset", ``, nil},
		{"string", `s:"x"`, "x"},
		{"bool", `b:true`, true},
		{"array", `arr:{v:{i:1} v:{s:"a"}}`, []any{int64(1), "a"}},
		{"vector", `vec:{f32:{v:1 v:2}}`, []float32{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeVal(parseText(t, tt.text, &luxirpb.Val{}))
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeVal = %#v, %v, want %#v", got, err, tt.want)
			}
		})
	}

	if got, err := decodeVal(parseText(t, `arr:{v:{docs:{}}}`, &luxirpb.Val{})); err == nil {
		t.Errorf("decodeVal(docs in an array) = %v, want an error", got)
	}
}

func TestOpResults_Accessors(t *testing.T) {
	r := OpResults{"f": &Facet{}, "d": &SearchResult{}, "i": int64(3), "x": 2.5, "n": nil}

	if r.Facet("f") == nil || r.Facet("d") != nil || r.Facet("missing") != nil || r.Facet("i") != nil {
		t.Error("Facet() must only return a *Facet")
	}
	if r.Docs("d") == nil || r.Docs("f") != nil || r.Docs("missing") != nil {
		t.Error("Docs() must only return a *SearchResult")
	}
	if v, ok := r.Float("i"); !ok || v != 3 {
		t.Errorf("Float(int) = %v, %v, want 3 widened", v, ok)
	}
	if v, ok := r.Float("x"); !ok || v != 2.5 {
		t.Errorf("Float(float) = %v, %v", v, ok)
	}
	for _, name := range []string{"n", "f", "missing"} {
		if _, ok := r.Float(name); ok {
			t.Errorf("Float(%q) ok, want false", name)
		}
	}
	if v, ok := r.Int("i"); !ok || v != 3 {
		t.Errorf("Int(int) = %v, %v", v, ok)
	}
	for _, name := range []string{"x", "n", "missing"} {
		if _, ok := r.Int(name); ok {
			t.Errorf("Int(%q) ok, want false", name)
		}
	}

	var nilResults OpResults
	if nilResults.Facet("a") != nil || nilResults.Docs("a") != nil {
		t.Error("a nil OpResults must be safe to read")
	}
}

func TestDecodeDocs(t *testing.T) {
	dl := parseText(t, `found:2 offset:5
		docs:{fields:{key:"id" value:{s:"a"}} fields:{key:"_score_" value:{f:1.5}}}
		docs:{fields:{key:"id" value:{s:"b"}} fields:{key:"_score_" value:{f:0.5}}}`, &luxirpb.DocList{})

	r, err := decodeDocs(dl)
	if err != nil {
		t.Fatalf("decodeDocs: %v", err)
	}
	if r.Found != 2 || r.Offset != 5 || r.uncounted || len(r.Hits) != 2 {
		t.Fatalf("result = %+v", r)
	}
	if r.Hits[0].Score != 1.5 || r.Hits[0].Doc["id"] != "a" || r.Hits[0].Doc["_score_"] != nil {
		t.Errorf("hit 0 = %+v, want score pulled out of the doc", r.Hits[0])
	}
}

func TestDecodeDocs_FoundAbsentVersusZero(t *testing.T) {
	if r, _ := decodeDocs(parseText(t, ``, &luxirpb.DocList{})); !r.uncounted {
		t.Error("uncounted = false, want true when found is absent")
	}
	if r, _ := decodeDocs(parseText(t, `found:0`, &luxirpb.DocList{})); r.uncounted || r.Found != 0 {
		t.Errorf("result = %+v, want counted zero", r)
	}
}

func TestDecodeDocs_Columns(t *testing.T) {
	dl := parseText(t, `row_count:1 columns:{key:"id" value:{col_s:{v:"a"}}}`, &luxirpb.DocList{})

	if r, err := decodeDocs(dl); err == nil {
		t.Errorf("decodeDocs = %+v, want an error: columns would lose documents silently", r)
	}
}

func TestDecodeDocs_BadHit(t *testing.T) {
	dl := parseText(t, `docs:{fields:{key:"id" value:{docs:{}}}}`, &luxirpb.DocList{})

	if _, err := decodeDocs(dl); err == nil || !strings.Contains(err.Error(), "hit 0") {
		t.Errorf("err = %v, want it to name hit 0", err)
	}
}

func TestSearch_BatchesMerge(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(
		hitsResp(true, withFound(docList("a", "b"), 5)),
		hitsResp(true, withFound(docList("c", "d"), 5)),
		wantResponse(t, `ops:{key:"hits" value:{docs:{found:5 docs:{fields:{key:"id" value:{s:"e"}}}
			ops:{key:"g" value:{facet:{bucket_ids:{col_s:{v:"x"}} counts:5}}}}}}`),
	))

	r, err := l.Search(timeout(t), "c", nil, WithLimit(-1), WithOp("g", FieldFacet("g_s")))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "e"}) {
		t.Errorf("ids = %v, want every batch in order", got)
	}
	if r.Found != 5 {
		t.Errorf("Found = %d, want 5", r.Found)
	}
	if f := r.Ops.Facet("g"); f == nil || f.Buckets[0].Count != 5 {
		t.Errorf("facet g = %+v, want the final batch's facet", f)
	}
}

func TestSearch_BatchesWithRootOps(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(
		hitsResp(true, docList("a")),
		opsResp(false, map[string]*luxirpb.Val{
			opName: {Kind: &luxirpb.Val_Docs{Docs: docList("b")}},
			"avg":  {Kind: &luxirpb.Val_D{D: 2.5}},
		}),
	))

	r, err := l.Search(timeout(t), "c", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("ids = %v", got)
	}
}

func TestSearch_ErrorAfterBatches(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(
		hitsResp(true, docList("a")),
		&luxirpb.SearchResponse{Error: &luxirpb.Error{Kind: luxirpb.Error_INVALID_REQUEST, Code: "bad", Message: "late failure"}},
	))

	r, err := l.Search(timeout(t), "c", nil)

	var se *SearchError
	if !errors.As(err, &se) || se.RequestFailed().GetCode() != "bad" {
		t.Fatalf("err = %v (%T), want *SearchError carrying Luxir's error", err, err)
	}
	if r != nil {
		t.Errorf("result = %+v, want nil: earlier batches are invalid after an error", r)
	}
}

func TestSearch_StreamEndsEarly(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(hitsResp(true, docList("a"))))

	r, err := l.Search(timeout(t), "c", nil)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want io.ErrUnexpectedEOF: more=true was never followed by a final batch", err)
	}
	if r != nil {
		t.Errorf("result = %+v, want nil", r)
	}
}

func TestSearch_WarningsFromEveryBatch(t *testing.T) {
	first := hitsResp(true, docList("a"))
	first.Warnings = []*luxirpb.Warning{{Code: "one"}}
	last := hitsResp(false, docList("b"))
	last.Warnings = []*luxirpb.Warning{{Code: "two"}}
	l, _ := fakeLuxir(t, fakeReply(first, last))

	r, err := l.Search(timeout(t), "c", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Warnings) != 2 || r.Warnings[0].GetCode() != "one" || r.Warnings[1].GetCode() != "two" {
		t.Errorf("Warnings = %v, want both in order", r.Warnings)
	}
}

func TestSearch_UndecodableResponses(t *testing.T) {
	tests := map[string]*luxirpb.SearchResponse{
		"columns":    hitsResp(false, parseText(t, `row_count:1 columns:{key:"id" value:{col_s:{v:"a"}}}`, &luxirpb.DocList{})),
		"bad hit":    hitsResp(false, parseText(t, `docs:{fields:{key:"id" value:{docs:{}}}}`, &luxirpb.DocList{})),
		"bad sub op": hitsResp(false, parseText(t, `ops:{key:"g" value:{facet:{bucket_ids:{col_s:{v:"a"}}}}}`, &luxirpb.DocList{})),
	}
	for name, resp := range tests {
		t.Run(name, func(t *testing.T) {
			l, _ := fakeLuxir(t, fakeReply(resp))

			r, err := l.Search(timeout(t), "c", nil)

			var se *SearchError
			if !errors.As(err, &se) || r != nil {
				t.Errorf("result = %v, err = %v (%T), want nil and *SearchError", r, err, err)
			}
			if errors.Unwrap(err) == nil {
				t.Error("Unwrap = nil, want the decode error")
			}
		})
	}
}

func TestSearch_CancelledMidStream(t *testing.T) {
	release := make(chan struct{})
	l, _ := fakeLuxir(t, func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error) {
		<-release
		return nil, nil
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := l.Search(ctx, "c", nil)

	if got := status.Code(err); got != codes.DeadlineExceeded {
		t.Errorf("status.Code(err) = %s, want DeadlineExceeded (err = %v)", got, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("Search did not return promptly after the deadline")
	}
}

func TestSearch_ServerErrorStatus(t *testing.T) {
	l, _ := fakeLuxir(t, func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error) {
		return nil, status.Error(codes.ResourceExhausted, "slow down")
	})

	_, err := l.Search(timeout(t), "c", nil)

	var se *SearchError
	if !errors.As(err, &se) || status.Code(err) != codes.ResourceExhausted {
		t.Errorf("err = %v, want *SearchError keeping the gRPC status", err)
	}
}
