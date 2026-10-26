package luxir

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func TestDelete(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "b1", "title_t": "Dune"},
		{"id": "b2", "title_t": "Emma"},
	}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if _, err := l.Delete(timeout(t), coll, []string{"b1"}, WithCommit()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 1 {
		t.Errorf("live docs = %d, want 1", got)
	}
}

func TestDelete_MissingID(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{{"id": "b1", "title_t": "Dune"}}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if _, err := l.Delete(timeout(t), coll, []string{"b1", "nope"}, WithCommit()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 0 {
		t.Errorf("live docs = %d, want 0", got)
	}
}

func TestDelete_Many(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "b1", "title_t": "Dune"},
		{"id": "b2", "title_t": "Emma"},
		{"id": "b3", "title_t": "Ulysses"},
	}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if _, err := l.Delete(timeout(t), coll, []string{"b1", "b3"}, WithCommit()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 1 {
		t.Errorf("live docs = %d, want 1", got)
	}
}

func TestDelete_NotCommitted(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.Index(timeout(t), coll, []Document{{"id": "b1", "title_t": "Dune"}}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if _, err := l.Delete(timeout(t), coll, []string{"b1"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 1 {
		t.Errorf("live docs = %d before commit, want 1", got)
	}
}

func TestDelete_NoIDs(t *testing.T) {
	l := &Luxir{}

	for name, ids := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			resp, err := l.Delete(context.Background(), "c", ids)

			var de *DeleteError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v (%T), want *DeleteError", err, err)
			}
			if resp != nil {
				t.Errorf("resp = %v, want nil", resp)
			}
			if !strings.Contains(err.Error(), "no provided ids") {
				t.Errorf("err = %q, want it to say no ids were given", err)
			}
		})
	}
}

func TestDelete_Unreachable(t *testing.T) {
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

	resp, err := l.Delete(timeout(t), "c", []string{"b1"})

	var de *DeleteError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want *DeleteError", err, err)
	}
	if resp != nil {
		t.Errorf("resp = %v, want nil when the call itself failed", resp)
	}
	if de.Err == nil {
		t.Error("DeleteError.Err is nil, want the gRPC cause")
	}
}

func TestDelete_KeepsCause(t *testing.T) {
	l := getClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := l.Delete(ctx, "c", []string{"b1"})

	var de *DeleteError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want *DeleteError", err, err)
	}
	if got := status.Code(err); got != codes.Canceled {
		t.Errorf("status.Code(err) = %s, want Canceled", got)
	}
}

func TestDeleteError_Error(t *testing.T) {
	cause := errors.New("boom")

	tests := []struct {
		name string
		err  *DeleteError
		want string
	}{
		{"msg", &DeleteError{Msg: "no provided ids"}, "[go-luxir] delete: no provided ids"},
		{"cause", &DeleteError{Err: cause}, "[go-luxir] delete: boom"},
		{"empty", &DeleteError{}, "[go-luxir] delete error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}

	if !errors.Is(&DeleteError{Err: cause}, cause) {
		t.Error("errors.Is(err, cause) = false, want Unwrap to expose the cause")
	}
}

func TestDelete_Fake(t *testing.T) {
	var got *luxirpb.UpdateRequest
	status := luxirpb.UpdateResponse_OK
	l := fakeServer(t, func(s *grpc.Server) {
		luxirpb.RegisterIndexerServer(s, &fakeIndexer{update: func(r *luxirpb.UpdateRequest) (*luxirpb.UpdateResponse, error) {
			got = r
			return &luxirpb.UpdateResponse{Status: status}, nil
		}})
	})

	if _, err := l.Delete(timeout(t), "", []string{"a"}, WithCommit()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertProto(t, got, parseText(t, `collection:"main" delete_ids:"a" commit:{}`, &luxirpb.UpdateRequest{}))

	status = luxirpb.UpdateResponse_ERROR
	resp, err := l.Delete(timeout(t), "c", []string{"a"})
	var de *DeleteError
	if !errors.As(err, &de) || de.Resp == nil || resp.GetStatus() != luxirpb.UpdateResponse_ERROR {
		t.Errorf("Delete = %v, %v, want a *DeleteError holding the ERROR response", resp, err)
	}
}
