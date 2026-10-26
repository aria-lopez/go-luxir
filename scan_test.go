package luxir

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func scanCollection(t *testing.T, l *Luxir, n int) string {
	t.Helper()
	coll := newCollection(t, l)
	if _, err := l.IndexStream(timeout(t), coll, slices.Values(numbered(n)), WithCommit()); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	return coll
}

func scanAll(t *testing.T, l *Luxir, coll string, q Query, opts ...SearchOption) ([]int64, error) {
	t.Helper()
	var ns []int64
	for h, err := range l.Scan(timeout(t), coll, q, opts...) {
		if err != nil {
			return ns, err
		}
		n, _ := h.Doc["n_i"].(int64)
		ns = append(ns, n)
	}
	return ns, nil
}

func span(from, to int64) []int64 {
	var out []int64
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

func TestScan_Everything(t *testing.T) {
	l := getClient(t)
	coll := scanCollection(t, l, 1200)

	got, err := scanAll(t, l, coll, nil, WithSort("n_i", false), WithBatchSize(100), WithFields("n_i"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !slices.Equal(got, span(0, 1200)) {
		t.Errorf("got %d hits, want all 1200 in order", len(got))
	}
}

func TestScan_Options(t *testing.T) {
	l := getClient(t)
	coll := scanCollection(t, l, 300)

	tests := []struct {
		name string
		q    Query
		opts []SearchOption
		want []int64
	}{
		{"offset and limit", nil, []SearchOption{WithSort("n_i", false), WithOffset(10), WithLimit(5)}, span(10, 15)},
		{"query", InRange("n_i", Gte(295)), []SearchOption{WithSort("n_i", true)}, []int64{299, 298, 297, 296, 295}},
		{"filter", nil, []SearchOption{WithFilter(InRange("n_i", Lt(3))), WithSort("n_i", false)}, span(0, 3)},
		{"limit zero", nil, []SearchOption{WithLimit(0)}, nil},
		{"nothing matches", Match("id", "nope"), nil, nil},
		{"request level", nil, []SearchOption{WithSort("n_i", false), WithLimit(2), WithFreshness(time.Second), WithTimeZone("UTC"), WithMaxParallel(1), WithRequestID("r"), WithScores(false)}, span(0, 2)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scanAll(t, l, coll, tt.q, tt.opts...)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("got %v, %v, want %v", got, err, tt.want)
			}
		})
	}
}

func TestScan_BreakEarly(t *testing.T) {
	l := getClient(t)
	coll := scanCollection(t, l, 600)

	n := 0
	for _, err := range l.Scan(timeout(t), coll, nil, WithBatchSize(50)) {
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if n++; n == 5 {
			break
		}
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
}

func TestScan_Rejected(t *testing.T) {
	l := getClient(t)
	coll := scanCollection(t, l, 3)

	_, err := scanAll(t, l, coll, InRange("nosuch", Gte(1)))

	var se *SearchError
	if !errors.As(err, &se) || se.RequestFailed() == nil {
		t.Errorf("err = %v, want *SearchError with Luxir's rejection", err)
	}
}

func TestScan_ClientErrorsYieldOnce(t *testing.T) {
	l := &Luxir{}
	tests := []struct {
		name string
		q    Query
		opt  SearchOption
		want string
	}{
		{"op", nil, WithOp("a", Avg("x")), "WithOp does not apply to Scan"},
		{"count", nil, WithCount(true), "WithCount does not apply to Scan"},
		{"profile", nil, WithProfile(), "WithProfile does not apply to Scan"},
		{"filter except", nil, WithFilterExcept(dune, "a"), "WithFilterExcept does not apply to Scan"},
		{"rrf", nil, WithRRF(1), "WithRRF does not apply to Scan"},
		{"bad offset", nil, WithOffset(-1), "WithOffset must not be negative"},
		{"bad query", Knn("v", nil, 1), nil, "vector is empty"},
		{"bad filter", nil, WithFilter(nil), "query is nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			for h, err := range l.Scan(context.Background(), "c", tt.q, tt.opt) {
				calls++
				var se *SearchError
				if !errors.As(err, &se) || !strings.Contains(err.Error(), tt.want) || h.Doc != nil {
					t.Errorf("yield = %v, %v, want *SearchError containing %q", h, err, tt.want)
				}
			}
			if calls != 1 {
				t.Errorf("yielded %d times, want once", calls)
			}
		})
	}
}

func TestScan_FakeRequest(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply(hitsResp(false, docList("a"))))

	for _, err := range l.Scan(timeout(t), "c", dune, WithBatchSize(7)) {
		if err != nil {
			t.Fatal(err)
		}
	}

	assertProto(t, f.last(t), wantRequest(t, `collection:"c" ops:{key:"hits" value:{top_docs:{query:{`+dunePB+`} limit:-1 get_scores:true batch_size:7 document_format:ROWS}}}`))
}

func TestScan_FakeBatchesThenError(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(
		hitsResp(true, docList("a", "b")),
		hitsResp(true, docList("c")),
		&luxirpb.SearchResponse{Error: &luxirpb.Error{Kind: luxirpb.Error_UNAVAILABLE, Message: "gone"}},
	))

	var got []string
	var last error
	for h, err := range l.Scan(timeout(t), "c", nil) {
		if err != nil {
			last = err
			break
		}
		got = append(got, h.Doc["id"].(string))
	}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("hits = %v, want every hit before the failure", got)
	}
	var se *SearchError
	if !errors.As(last, &se) || se.RequestFailed().GetMessage() != "gone" {
		t.Errorf("err = %v, want Luxir's error", last)
	}
}

func TestScan_FakeEndsEarly(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(hitsResp(true, docList("a"))))

	_, err := scanAll(t, l, "c", nil)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestScan_FakeColumnsAndBadHit(t *testing.T) {
	cols := docList()
	cols.Columns = map[string]*luxirpb.Column{"id": {}}
	bad := &luxirpb.DocList{Docs: []*luxirpb.Map{{Fields: map[string]*luxirpb.Val{"x": {Kind: &luxirpb.Val_Docs{}}}}}}

	for name, d := range map[string]*luxirpb.DocList{"columns": cols, "bad hit": bad} {
		l, _ := fakeLuxir(t, fakeReply(hitsResp(false, d)))
		_, err := scanAll(t, l, "c", nil)
		var se *SearchError
		if !errors.As(err, &se) {
			t.Errorf("%s: err = %v, want *SearchError", name, err)
		}
	}
}

func TestScan_BadQueryYieldsOnce(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply())
	n := 0
	for _, err := range l.Scan(timeout(t), "c", Bool()) {
		var se *SearchError
		if !errors.As(err, &se) || !strings.Contains(err.Error(), "no clauses") {
			t.Errorf("err = %v, want the query's *SearchError", err)
		}
		n++
	}
	if n != 1 || f.calls() != 0 {
		t.Errorf("yielded %d times and sent %d requests, want 1 and 0", n, f.calls())
	}
}

func TestScan_SendFailsYieldsOnce(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply())
	n := 0
	for _, err := range l.Scan(timeout(t), "bad\xff", nil) {
		var se *SearchError
		if !errors.As(err, &se) {
			t.Errorf("err = %v, want a *SearchError", err)
		}
		n++
	}
	if n != 1 || f.calls() != 0 {
		t.Errorf("yielded %d times and sent %d requests, want 1 and 0", n, f.calls())
	}
}
