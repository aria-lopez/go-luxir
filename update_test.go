package luxir

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func TestUpdateOptions_Request(t *testing.T) {
	tests := []struct {
		name string
		opts []UpdateOption
		want string
	}{
		{"none", nil, ``},
		{"commit", []UpdateOption{WithCommit()}, `commit:{}`},
		{"commit within", []UpdateOption{WithCommitWithin(1500 * time.Millisecond)}, `commit:{commit_within_ms:1500}`},
		{"commit within negative is now", []UpdateOption{WithCommitWithin(-time.Second)}, `commit:{}`},
		{"wait for merges", []UpdateOption{WithWaitForMerges()}, `commit:{wait_for_merges:true}`},
		{"max segments", []UpdateOption{WithMaxSegments(1)}, `commit:{max_segments:1}`},
		{"aux indexes", []UpdateOption{WithBuildAuxIndexes("*"), WithBuildAuxIndexes("vec.a_v")}, `commit:{build_aux_indexes:"*" build_aux_indexes:"vec.a_v"}`},
		{"commit options stack", []UpdateOption{WithCommitWithin(time.Second), WithCommit(), WithMaxSegments(2)},
			`commit:{commit_within_ms:1000 max_segments:2}`},
		{"all or none", []UpdateOption{WithAllOrNone()}, `all_or_none:true`},
		{"allow dups", []UpdateOption{WithAllowDups()}, `allow_dups:true`},
		{"return ids", []UpdateOption{WithReturnIDs()}, `return_ids:true`},
		{"field map", []UpdateOption{WithFieldMap(map[string]string{"t": "title_t"}), WithFieldMap(map[string]string{"x": ""})},
			`field_map:{key:"t" value:"title_t"} field_map:{key:"x" value:""}`},
		{"drop unmapped", []UpdateOption{WithFieldMap(map[string]string{"id": "id"}), WithDropUnmapped()},
			`field_map:{key:"id" value:"id"} drop_unmapped:true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := &luxirpb.UpdateRequest{}
			for _, o := range tt.opts {
				o(got)
			}
			assertProto(t, got, parseText(t, tt.want, &luxirpb.UpdateRequest{}))
		})
	}
}

func TestUpdateOptions_FieldMapCopies(t *testing.T) {
	m := map[string]string{"t": "title_t"}
	r := &luxirpb.UpdateRequest{}
	WithFieldMap(m)(r)
	r.FieldMap["x"] = "y"

	if len(m) != 1 {
		t.Errorf("caller's map = %v, want it left alone", m)
	}
}

func TestCommit(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.Index(timeout(t), coll, []Document{{"id": "a", "title_t": "Dune"}}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 0 {
		t.Fatalf("live docs = %d before Commit, want 0", got)
	}
	resp, err := l.Commit(timeout(t), coll)
	if err != nil || resp.GetStatus() != luxirpb.UpdateResponse_OK {
		t.Fatalf("Commit = %v, %v", resp, err)
	}
	if got := liveDocs(t, l, coll); got != 1 {
		t.Errorf("live docs = %d after Commit, want 1", got)
	}
}

func TestCommit_Options(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.Index(timeout(t), coll, []Document{{"id": "a", "title_t": "Dune"}}, WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if _, err := l.Index(timeout(t), coll, []Document{{"id": "b", "title_t": "Emma"}}, WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if _, err := l.Commit(timeout(t), coll, WithWaitForMerges(), WithMaxSegments(1), WithBuildAuxIndexes("*")); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	st, err := l.Stats(timeout(t), coll, false)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if tot := st.GetCollections()[0].GetTotals(); tot.GetLiveDocs() != 2 || tot.GetCommittedSegments() != 1 {
		t.Errorf("totals = %v, want 2 live docs merged into 1 segment", tot)
	}
}

func TestIndex_CommitWithin(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.Index(timeout(t), coll, []Document{{"id": "a", "title_t": "Dune"}}, WithCommitWithin(50*time.Millisecond)); err != nil {
		t.Fatalf("Index: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for liveDocs(t, l, coll) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("document never became visible")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestIndex_ReturnIDs(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	resp, err := l.Index(timeout(t), coll, []Document{{"id": "b", "t_s": "x"}, {"id": "a", "t_s": "y"}}, WithReturnIDs())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if !reflect.DeepEqual(resp.GetIds(), []string{"b", "a"}) {
		t.Errorf("ids = %v, want request order", resp.GetIds())
	}
}

func TestIndex_AllOrNone(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{{"id": "good", "t_s": "x"}, {"id": "bad", "nosuffix": "x"}}, WithAllOrNone(), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || ie.Status() != luxirpb.UpdateResponse_ERROR {
		t.Fatalf("err = %v, want *IndexError with ERROR", err)
	}
	if got := liveDocs(t, l, coll); got != 0 {
		t.Errorf("live docs = %d, want the good document rolled back", got)
	}
}

func TestIndex_AllowDups(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	for i := 0; i < 2; i++ {
		if _, err := l.Index(timeout(t), coll, []Document{{"id": "a", "t_s": "x"}}, WithAllowDups(), WithCommit()); err != nil {
			t.Fatalf("Index: %v", err)
		}
	}
	if got := liveDocs(t, l, coll); got != 2 {
		t.Errorf("live docs = %d, want 2: allow dups skips the replace", got)
	}
}

func TestIndex_FieldMap(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{{"key": "a", "title": "Dune", "junk": "x"}},
		WithFieldMap(map[string]string{"key": "id", "title": "title_t"}), WithDropUnmapped(), WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	r, err := l.Search(timeout(t), coll, Match("title_t", "dune"))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if r.Found != 1 || r.Hits[0].Doc["id"] != "a" || r.Hits[0].Doc["junk"] != nil {
		t.Errorf("hits = %+v, want one doc renamed onto id and title_t, junk dropped", r.Hits)
	}
}

func TestIndex_TooBigForOneMessage(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, bigDocs(5, 1<<20))

	var ie *IndexError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "larger than max") {
		t.Errorf("err = %v, want Luxir's message size rejection", err)
	}
}

func TestIndexError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *IndexError
		want string
	}{
		{"msg", &IndexError{Msg: "err on document: 1: x"}, "[go-luxir] index: err on document: 1: x"},
		{"request failed", &IndexError{Resp: &luxirpb.UpdateResponse{Status: luxirpb.UpdateResponse_ERROR, Error: &luxirpb.Error{Message: "commit failed"}}},
			"[go-luxir] index ERROR: commit failed"},
		{"documents failed", &IndexError{Resp: &luxirpb.UpdateResponse{Status: luxirpb.UpdateResponse_PARTIAL, TotalErrors: 2}},
			"[go-luxir] index PARTIAL: 2 document(s) failed"},
		{"cause", &IndexError{Err: errors.New("boom")}, "[go-luxir] index: boom"},
		{"empty", &IndexError{}, "[go-luxir] index error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorTypes_Text(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"delete request failed", &DeleteError{Resp: &luxirpb.UpdateResponse{Status: luxirpb.UpdateResponse_ERROR, Error: &luxirpb.Error{Message: "disk"}}}, "[go-luxir] delete ERROR: disk"},
		{"match cause", &MatchError{Err: cause}, "[go-luxir] match: boom"},
		{"match msg", &MatchError{Msg: "m", Err: cause}, "m"},
		{"search msg", &SearchError{Msg: "m"}, "[go-luxir] search: m"},
		{"search luxir", &SearchError{Luxir: &luxirpb.Error{Kind: luxirpb.Error_UNAVAILABLE, Code: "c", Message: "down"}}, "[go-luxir] search UNAVAILABLE (c): down"},
		{"search cause", &SearchError{Err: cause}, "[go-luxir] search: boom"},
		{"search empty", &SearchError{}, "[go-luxir] search error"},
		{"op cause", &OpError{Op: "a", Err: cause}, `[go-luxir] op "a": boom`},
		{"query cause", &QueryError{Query: "knn", Err: cause}, "[go-luxir] knn query: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}

	if (&DeleteError{Resp: &luxirpb.UpdateResponse{Error: &luxirpb.Error{Code: "x"}}}).RequestFailed().GetCode() != "x" {
		t.Error("DeleteError.RequestFailed lost Luxir's error")
	}
	for _, err := range []error{&MatchError{Err: cause}, &SearchError{Err: cause}, &OpError{Err: cause}, &QueryError{Err: cause}} {
		if !errors.Is(err, cause) {
			t.Errorf("%T does not unwrap to its cause", err)
		}
	}
}
