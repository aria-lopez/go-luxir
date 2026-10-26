package luxir

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func collectionNames(t *testing.T, l *Luxir) []string {
	t.Helper()

	colls, err := l.GetCollections(timeout(t))
	if err != nil {
		t.Fatalf("GetCollections: %v", err)
	}

	names := make([]string, 0, len(colls))
	for _, c := range colls {
		names = append(names, c.GetName())
	}
	return names
}

func TestCollections_Lifecycle(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	if err := l.CreateCollection(timeout(t), name, nil); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if !slices.Contains(collectionNames(t, l), name) {
		t.Errorf("GetCollections() missing %q after create", name)
	}

	if err := l.DeleteCollection(timeout(t), name); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if slices.Contains(collectionNames(t, l), name) {
		t.Errorf("GetCollections() still has %q after delete", name)
	}
}

func TestCreateCollection_Exists(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	if err := l.CreateCollection(timeout(t), name, nil); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	err := l.CreateCollection(timeout(t), name, nil)

	var ce *CollectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CollectionError", err, err)
	}
	if !errors.Is(err, ErrCollectionExists) {
		t.Errorf("errors.Is(err, ErrCollectionExists) = false, err = %v", err)
	}
	if errors.Is(err, ErrCollectionNotFound) {
		t.Error("errors.Is(err, ErrCollectionNotFound) = true, want false")
	}
	if ce.Op != "create" || ce.Name != name {
		t.Errorf("Op = %q, Name = %q, want create and %q", ce.Op, ce.Name, name)
	}
}

func TestDeleteCollection_NotFound(t *testing.T) {
	l := getClient(t)
	name := newCollection(t, l)

	err := l.DeleteCollection(timeout(t), name)

	var ce *CollectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CollectionError", err, err)
	}
	if !errors.Is(err, ErrCollectionNotFound) {
		t.Errorf("errors.Is(err, ErrCollectionNotFound) = false, err = %v", err)
	}
}

func TestDeleteCollection_DropsDocs(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{{"id": "b1", "title_t": "Dune"}}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 1 {
		t.Fatalf("live docs = %d, want 1", got)
	}

	if err := l.DeleteCollection(timeout(t), coll); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if err := l.CreateCollection(timeout(t), coll, nil); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 0 {
		t.Errorf("live docs = %d after recreate, want 0", got)
	}
}

func TestGetCollections_Stats(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "b1", "title_t": "Dune"},
		{"id": "b2", "title_t": "Emma"},
	}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	colls, err := l.GetCollections(timeout(t))
	if err != nil {
		t.Fatalf("GetCollections: %v", err)
	}

	var found *luxirpb.CollectionStats
	for _, c := range colls {
		if c.GetName() == coll {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("GetCollections() missing %q", coll)
	}
	if got := found.GetTotals().GetLiveDocs(); got != 2 {
		t.Errorf("live docs = %d, want 2", got)
	}
}

func TestCreateCollection_NoName(t *testing.T) {
	l := &Luxir{}

	err := l.CreateCollection(context.Background(), "", nil)

	var ce *CollectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CollectionError", err, err)
	}
	if ce.Op != "create" {
		t.Errorf("Op = %q, want create", ce.Op)
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %q, want it to say the name is required", err)
	}
}

func TestDeleteCollection_NoName(t *testing.T) {
	l := &Luxir{}

	err := l.DeleteCollection(context.Background(), "")

	var ce *CollectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CollectionError", err, err)
	}
	if ce.Op != "delete" {
		t.Errorf("Op = %q, want delete", ce.Op)
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %q, want it to say the name is required", err)
	}
}

func TestCollections_Unreachable(t *testing.T) {
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

	t.Run("create", func(t *testing.T) {
		err := l.CreateCollection(timeout(t), "c", nil)

		var ce *CollectionError
		if !errors.As(err, &ce) || ce.Err == nil {
			t.Fatalf("err = %v (%T), want *CollectionError with the gRPC cause", err, err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		err := l.DeleteCollection(timeout(t), "c")

		var ce *CollectionError
		if !errors.As(err, &ce) || ce.Err == nil {
			t.Fatalf("err = %v (%T), want *CollectionError with the gRPC cause", err, err)
		}
	})
	t.Run("list", func(t *testing.T) {
		colls, err := l.GetCollections(timeout(t))

		var ce *CollectionError
		if !errors.As(err, &ce) || ce.Err == nil {
			t.Fatalf("err = %v (%T), want *CollectionError with the gRPC cause", err, err)
		}
		if colls != nil {
			t.Errorf("colls = %v, want nil", colls)
		}
	})
}

func TestCollections_KeepsCause(t *testing.T) {
	l := getClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := l.CreateCollection(ctx, "c", nil)

	var ce *CollectionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CollectionError", err, err)
	}
	if got := status.Code(err); got != codes.Canceled {
		t.Errorf("status.Code(err) = %s, want Canceled", got)
	}
}

func TestCollectionError_Is(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantExists   bool
		wantNotFound bool
	}{
		{"already exists", status.Error(codes.AlreadyExists, "x"), true, false},
		{"not found", status.Error(codes.NotFound, "x"), false, true},
		{"other code", status.Error(codes.Internal, "x"), false, false},
		{"not a status", errors.New("x"), false, false},
		{"nil", nil, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := &CollectionError{Op: "create", Name: "c", Err: tt.err}

			if got := errors.Is(ce, ErrCollectionExists); got != tt.wantExists {
				t.Errorf("Is(ErrCollectionExists) = %v, want %v", got, tt.wantExists)
			}
			if got := errors.Is(ce, ErrCollectionNotFound); got != tt.wantNotFound {
				t.Errorf("Is(ErrCollectionNotFound) = %v, want %v", got, tt.wantNotFound)
			}
		})
	}
}

func TestCollectionError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *CollectionError
		want string
	}{
		{"msg", &CollectionError{Op: "create", Msg: "name is required"}, "[go-luxir] collection create: name is required"},
		{"cause", &CollectionError{Op: "delete", Name: "books", Err: errors.New("boom")}, `[go-luxir] collection delete "books": boom`},
		{"no name", &CollectionError{Op: "stats", Err: errors.New("boom")}, `[go-luxir] collection stats: boom`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
