package luxir

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// newCollection ;; a unique collection name for one test, deleted when the test ends
//
// ;; a fresh name per test keeps tests from seeing each other's documents
func newCollection(t *testing.T, l *Luxir) string {
	t.Helper()

	name := fmt.Sprintf("go_luxir_test_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// ;; the collection only exists if the test wrote to it, so ignore NOT_FOUND
		luxirpb.NewAdminClient(l.conn).DeleteCollection(ctx, &luxirpb.DeleteCollectionRequest{Name: name})
	})

	return name
}

// liveDocs ;; committed, undeleted document count Luxir reports for a collection
//
// ;; there is no Search method yet, so Stats is how the tests see what landed
func liveDocs(t *testing.T, l *Luxir, collection string) uint64 {
	t.Helper()

	resp, err := luxirpb.NewAdminClient(l.conn).Stats(timeout(t), &luxirpb.StatsRequest{Collection: collection})
	if err != nil {
		t.Fatalf("Stats(%q): %v", collection, err)
	}
	if len(resp.GetCollections()) == 0 {
		return 0
	}
	return resp.GetCollections()[0].GetTotals().GetLiveDocs()
}

func TestIndex_Commit(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	resp, err := l.Index(timeout(t), coll, []Document{
		{"id": "b1", "title_t": "Dune", "year_i": 1965},
		{"id": "b2", "title_t": "Dune Messiah", "year_i": 1969, "tags_ss": []string{"scifi"}},
	}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if resp.GetStatus() != luxirpb.UpdateResponse_OK {
		t.Errorf("status = %s, want OK", resp.GetStatus())
	}
	if got := liveDocs(t, l, coll); got != 2 {
		t.Errorf("live docs = %d, want 2", got)
	}
}

// TestIndex_ReplacesByID ;; the same id twice is one document, not two
func TestIndex_ReplacesByID(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	for _, title := range []string{"Dune", "Dune (revised)"} {
		if _, err := l.Index(timeout(t), coll, []Document{{"id": "b1", "title_t": title}}, WithCommit()); err != nil {
			t.Fatalf("Index %q: %v", title, err)
		}
	}

	if got := liveDocs(t, l, coll); got != 1 {
		t.Errorf("live docs = %d, want 1", got)
	}
}

// TestIndex_Partial ;; one good and one bad document: PARTIAL, and the good one is kept
func TestIndex_Partial(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "good", "title_t": "Dune"},
		{"id": "bad", "nosuffix": "x"}, // ;; no type suffix, so Luxir rejects it
	}, WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v (%T), want *IndexError", err, err)
	}
	if !ie.IsPartial() {
		t.Errorf("status = %s, want PARTIAL", ie.Status())
	}

	failed := ie.Failed()
	if len(failed) != 1 {
		t.Fatalf("got %d failed docs, want 1", len(failed))
	}
	if f := failed[0]; f.GetIndex() != 1 || f.GetId() != "bad" || f.GetError().GetCode() != "unknown_field" {
		t.Errorf("failed[0] = index %d, id %q, code %q", f.GetIndex(), f.GetId(), f.GetError().GetCode())
	}
	if ie.RequestFailed() != nil {
		t.Errorf("RequestFailed() = %v, want nil (failure was per-document)", ie.RequestFailed())
	}
	if got := liveDocs(t, l, coll); got != 1 {
		t.Errorf("live docs = %d, want 1 (the good document)", got)
	}
}

// TestIndex_AllBad ;; every document fails: ERROR, nothing indexed
func TestIndex_AllBad(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{{"id": "bad", "nosuffix": "x"}}, WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v (%T), want *IndexError", err, err)
	}
	if ie.Status() != luxirpb.UpdateResponse_ERROR {
		t.Errorf("status = %s, want ERROR", ie.Status())
	}
	if got := liveDocs(t, l, coll); got != 0 {
		t.Errorf("live docs = %d, want 0", got)
	}
}

// TestIndex_NotCommitted ;; without WithCommit the document is written but not yet visible
func TestIndex_NotCommitted(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.Index(timeout(t), coll, []Document{{"id": "b1", "title_t": "Dune"}}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if got := liveDocs(t, l, coll); got != 0 {
		t.Errorf("live docs = %d before commit, want 0", got)
	}
}

// TestIndex_ConvertError ;; a value we can't convert fails before any network call
//
// ;; &Luxir{} has no connection at all: if Index touched the network this would panic
func TestIndex_ConvertError(t *testing.T) {
	l := &Luxir{}

	_, err := l.Index(context.Background(), "c", []Document{
		{"id": "ok", "title_t": "fine"},
		{"id": "bad", "x_s": struct{}{}},
	})

	var ie *IndexError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v (%T), want *IndexError", err, err)
	}
}

// TestIndex_Unreachable ;; no Luxir listening: an error comes back, and no response
func TestIndex_Unreachable(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close() // ;; free port with nothing on it

	l, err := NewLuxir(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.conn.Close() })

	resp, err := l.Index(timeout(t), "c", []Document{{"id": "1", "t_s": "x"}})
	if err == nil {
		t.Fatalf("Index = %v, want an error", resp)
	}
	if resp != nil {
		t.Errorf("resp = %v, want nil when the call itself failed", resp)
	}
}

// TestIndex_KeepsCause ;; the real gRPC error survives inside *IndexError
//
// ;; errors.Is(err, context.Canceled) is false for gRPC errors: they carry their own
// ;; status type. status.Code looks through IndexError.Unwrap and finds it.
func TestIndex_KeepsCause(t *testing.T) {
	l := getClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ;; already cancelled, so the call fails before reaching Luxir

	_, err := l.Index(ctx, "c", []Document{{"id": "1", "t_s": "x"}})

	var ie *IndexError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v (%T), want *IndexError", err, err)
	}
	if ie.Err == nil {
		t.Fatal("IndexError.Err is nil, want the gRPC cause")
	}
	if got := status.Code(err); got != codes.Canceled {
		t.Errorf("status.Code(err) = %s, want Canceled", got)
	}
}

// TestIndex_ConvertErrorKeepsCause ;; the conversion error is reachable through Unwrap
func TestIndex_ConvertErrorKeepsCause(t *testing.T) {
	l := &Luxir{}

	_, err := l.Index(context.Background(), "c", []Document{{"id": "bad", "x_s": struct{}{}}})

	if errors.Unwrap(err) == nil {
		t.Fatalf("errors.Unwrap(%v) = nil, want the conversion error", err)
	}
	if !strings.Contains(err.Error(), "document: 0") {
		t.Errorf("err = %q, want it to say which document failed", err)
	}
}
