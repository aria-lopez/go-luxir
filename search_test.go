package luxir

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// seed ;; a fresh collection holding three books, committed
func seed(t *testing.T, l *Luxir) string {
	t.Helper()
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "b1", "title_t": "Dune", "year_i": 1965, "tags_ss": []string{"scifi", "classic"}},
		{"id": "b2", "title_t": "Dune Messiah", "year_i": 1969},
		{"id": "b3", "title_t": "Neuromancer", "year_i": 1984},
	}, WithCommit())
	if err != nil {
		t.Fatalf("seed Index: %v", err)
	}

	return coll
}

// ids ;; the "id" of every hit, in order
func ids(r *SearchResult) []string {
	out := make([]string, len(r.Hits))
	for i, h := range r.Hits {
		out[i], _ = h.Doc["id"].(string)
	}
	return out
}

// TestFromVal_RoundTrip ;; what toVal sends, fromVal reads back (ints widen to int64)
func TestFromVal_RoundTrip(t *testing.T) {
	in := Document{
		"s": "x", "b": true, "i": 7, "i64": int64(8), "f": float32(1.5), "d": 2.5,
		"bin": []byte("hi"), "ss": []string{"a", "b"}, "is": []int{1, 2}, "ds": []float64{1, 2},
		"mixed": []any{"a", 3}, "mv": [][]float32{{1, 2}, {3}}, "pt": Point{Lat: 1, Lon: 2},
	}
	want := Document{
		"s": "x", "b": true, "i": int64(7), "i64": int64(8), "f": float32(1.5), "d": 2.5,
		"bin": []byte("hi"), "ss": []string{"a", "b"}, "is": []int64{1, 2}, "ds": []float64{1, 2},
		"mixed": []any{"a", int64(3)}, "mv": [][]float32{{1, 2}, {3}}, "pt": []float64{2, 1},
	}

	m, err := in.toMap()
	if err != nil {
		t.Fatal(err)
	}
	got, err := fromMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %#v\nwant %#v", got, want)
	}
}

func TestSearch_Match(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, Match("title_t", "dune"))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if r.Found != 2 || len(r.Hits) != 2 {
		t.Fatalf("found = %d, hits = %d, want 2 and 2", r.Found, len(r.Hits))
	}
	if r.Hits[0].Score <= 0 {
		t.Errorf("score = %v, want > 0", r.Hits[0].Score)
	}
	if _, ok := r.Hits[0].Doc["_score_"]; ok {
		t.Error("_score_ leaked into Doc, want it as Hit.Score only")
	}
	// ;; the shorter title ranks first
	if got := r.Hits[0].Doc["title_t"]; got != "Dune" {
		t.Errorf("top hit title = %v, want Dune", got)
	}
	if got := r.Hits[0].Doc["year_i"]; got != int64(1965) {
		t.Errorf("year_i = %#v, want int64(1965)", got)
	}
	if got := r.Hits[0].Doc["tags_ss"]; !reflect.DeepEqual(got, []string{"classic", "scifi"}) {
		t.Errorf("tags_ss = %#v", got)
	}
}

func TestSearch_Simple(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, Simple("neuro* -dune", "title_t"))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"b3"}) {
		t.Errorf("ids = %v, want [b3]", got)
	}
}

// TestSearch_All ;; a nil query matches everything
func TestSearch_All(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 3 || len(r.Hits) != 3 {
		t.Errorf("found = %d, hits = %d, want 3 and 3", r.Found, len(r.Hits))
	}
}

// TestSearch_Paging ;; Found is every match even when the page is smaller
func TestSearch_Paging(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	var all []string
	for off := int64(0); off < 3; off++ {
		r, err := l.Search(timeout(t), coll, nil, WithSort("year_i", false), WithLimit(1), WithOffset(off))
		if err != nil {
			t.Fatalf("Search offset %d: %v", off, err)
		}
		if r.Found != 3 || len(r.Hits) != 1 {
			t.Fatalf("offset %d: found = %d, hits = %d, want 3 and 1", off, r.Found, len(r.Hits))
		}
		all = append(all, ids(r)...)
	}
	if !reflect.DeepEqual(all, []string{"b1", "b2", "b3"}) {
		t.Errorf("paged ids = %v, want [b1 b2 b3]", all)
	}
}

func TestSearch_SortDesc(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, nil, WithSort("year_i", true))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"b3", "b2", "b1"}) {
		t.Errorf("ids = %v, want [b3 b2 b1]", got)
	}
}

func TestSearch_Fields(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, Match("title_t", "neuromancer"), WithFields("title_t"))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(r.Hits))
	}
	if got := r.Hits[0].Doc; !reflect.DeepEqual(got, Document{"title_t": "Neuromancer"}) {
		t.Errorf("Doc = %#v, want only title_t", got)
	}
}

func TestSearch_Filter(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, Match("title_t", "dune"), WithFilter(Match("year_i", 1969)))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"b2"}) {
		t.Errorf("ids = %v, want [b2]", got)
	}
}

// TestSearch_Raw ;; the escape hatch takes any protobuf query
func TestSearch_Raw(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, Raw(&luxirpb.Query{Kind: &luxirpb.Query_All{All: true}}))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 3 {
		t.Errorf("found = %d, want 3", r.Found)
	}
}

// TestSearch_BatchedMerged ;; more documents than one streamed batch come back as one result
func TestSearch_BatchedMerged(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	docs := make([]Document, 250)
	for i := range docs {
		docs[i] = Document{"id": string(rune('A'+i/26)) + string(rune('a'+i%26)), "n_i": i}
	}
	if _, err := l.Index(timeout(t), coll, docs, WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	r, err := l.Search(timeout(t), coll, nil, WithLimit(-1), WithSort("n_i", false))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 250 || len(r.Hits) != 250 {
		t.Fatalf("found = %d, hits = %d, want 250 and 250", r.Found, len(r.Hits))
	}
	for i, h := range r.Hits {
		if h.Doc["n_i"] != int64(i) {
			t.Fatalf("hit %d has n_i = %v, batches out of order or duplicated", i, h.Doc["n_i"])
		}
	}
}

// TestSearch_UnknownField ;; Luxir says no: *SearchError carrying Luxir's kind, code and message
func TestSearch_UnknownField(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	r, err := l.Search(timeout(t), coll, Match("nosuffix", "x"))

	var se *SearchError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *SearchError", err, err)
	}
	if r != nil {
		t.Errorf("result = %v, want nil: no partial results", r)
	}
	if se.RequestFailed() == nil {
		t.Fatalf("RequestFailed() = nil, want Luxir's error (err = %q)", err)
	}
	if got := se.RequestFailed(); got.GetKind() != luxirpb.Error_INVALID_REQUEST || got.GetCode() != "unknown_field" {
		t.Errorf("kind = %s, code = %q, want INVALID_REQUEST and unknown_field", got.GetKind(), got.GetCode())
	}
	// ;; the readable message names the bad field, an empty error here means Luxir's reason was swallowed
	if !strings.Contains(err.Error(), "nosuffix") {
		t.Errorf("err = %q, want it to say which field Luxir rejected", err)
	}
}

// TestSearch_ConvertError ;; a value we can't convert fails before any network call
//
// ;; &Luxir{} has no connection at all: if Search touched the network this would panic
func TestSearch_ConvertError(t *testing.T) {
	l := &Luxir{}

	for name, call := range map[string]func() error{
		"query": func() error { _, err := l.Search(context.Background(), "c", Match("x_s", struct{}{})); return err },
		"filter": func() error {
			_, err := l.Search(context.Background(), "c", nil, WithFilter(Match("x_s", struct{}{})))
			return err
		},
	} {
		err := call()
		var se *SearchError
		if !errors.As(err, &se) {
			t.Fatalf("%s: err = %v (%T), want *SearchError", name, err, err)
		}
		if errors.Unwrap(err) == nil {
			t.Errorf("%s: Unwrap = nil, want the conversion error", name)
		}
		if strings.Contains(err.Error(), "%!") {
			t.Errorf("%s: err = %q, has a broken format verb", name, err)
		}
		var me *MatchError
		if !errors.As(err, &me) {
			t.Errorf("%s: errors.As(*MatchError) = false, want the cause kept", name)
		}
	}
}

// TestSearch_Unreachable ;; no Luxir listening: an error comes back, and no result
func TestSearch_Unreachable(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()

	l, err := NewLuxir(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.conn.Close() })

	r, err := l.Search(timeout(t), "c", nil)
	if err == nil {
		t.Fatalf("Search = %v, want an error", r)
	}
	if r != nil {
		t.Errorf("result = %v, want nil when the call itself failed", r)
	}
	if err.Error() == "" {
		t.Error("err.Error() is empty, want the transport failure described")
	}
}

// TestSearch_KeepsCause ;; the real gRPC error survives inside *SearchError
func TestSearch_KeepsCause(t *testing.T) {
	l := getClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := l.Search(ctx, "c", nil)

	var se *SearchError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *SearchError", err, err)
	}
	if got := status.Code(err); got != codes.Canceled {
		t.Errorf("status.Code(err) = %s, want Canceled", got)
	}
}

// TestSearchResult_Paging ;; HasMore and NextOffset from the numbers alone, no network
func TestSearchResult_Paging(t *testing.T) {
	hits := func(n int) []Hit { return make([]Hit, n) }

	for name, tc := range map[string]struct {
		r       SearchResult
		hasMore bool
		nextOff int64
	}{
		"first of three":  {SearchResult{Found: 250, Offset: 0, Hits: hits(100)}, true, 100},
		"middle":          {SearchResult{Found: 250, Offset: 100, Hits: hits(100)}, true, 200},
		"short last page": {SearchResult{Found: 250, Offset: 200, Hits: hits(50)}, false, 250},
		"exact multiple":  {SearchResult{Found: 200, Offset: 100, Hits: hits(100)}, false, 200},
		"everything":      {SearchResult{Found: 3, Offset: 0, Hits: hits(3)}, false, 3},
		"no matches":      {SearchResult{Found: 0}, false, 0},
		"past the end":    {SearchResult{Found: 3, Offset: 10}, false, 10},
		"limit 0":         {SearchResult{Found: 3, Offset: 0}, false, 0}, // ;; empty page can't advance, must not loop forever
	} {
		if got := tc.r.HasMore(); got != tc.hasMore {
			t.Errorf("%s: HasMore() = %v, want %v", name, got, tc.hasMore)
		}
		if got := tc.r.NextOffset(); got != tc.nextOff {
			t.Errorf("%s: NextOffset() = %d, want %d", name, got, tc.nextOff)
		}
	}
}

// TestSearch_WalkPages ;; the documented loop visits every document once
func TestSearch_WalkPages(t *testing.T) {
	l := getClient(t)
	coll := seed(t, l)

	var all []string
	var pages int
	for off := int64(0); ; {
		pages++
		r, err := l.Search(timeout(t), coll, nil, WithSort("year_i", false), WithLimit(2), WithOffset(off))
		if err != nil {
			t.Fatalf("Search offset %d: %v", off, err)
		}
		if r.Offset != off {
			t.Errorf("Offset = %d, want %d", r.Offset, off)
		}
		all = append(all, ids(r)...)
		if !r.HasMore() {
			break
		}
		off = r.NextOffset()
	}

	if pages != 2 { // ;; 3 documents, 2 per page
		t.Errorf("pages = %d, want 2", pages)
	}
	if !reflect.DeepEqual(all, []string{"b1", "b2", "b3"}) {
		t.Errorf("ids = %v, want [b1 b2 b3]", all)
	}
}

// TestSearch_SendFails ;; a request gRPC can't encode (strings must be UTF-8) or a send side that won't close
func TestSearch_SendFails(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	_, err := l.Search(timeout(t), "bad\xff", nil)
	var se *SearchError
	if !errors.As(err, &se) || status.Code(se.Err) != codes.Internal {
		t.Errorf("Search = %v, want a *SearchError with the marshal failure", err)
	}
	if f.calls() != 0 {
		t.Errorf("server got %d requests, want none", f.calls())
	}

	closeErr := errors.New("close refused")
	l, _ = fakeLuxirWith(t, fakeReply(&luxirpb.SearchResponse{}), WithDialOptions(grpc.WithStreamInterceptor(
		func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			s, err := streamer(ctx, desc, cc, method, opts...)
			return closeFails{s, closeErr}, err
		})))
	if _, err := l.Search(timeout(t), "c", nil); !errors.Is(err, closeErr) {
		t.Errorf("Search = %v, want the CloseSend error", err)
	}
}

type closeFails struct {
	grpc.ClientStream
	err error
}

func (c closeFails) CloseSend() error { return c.err }
