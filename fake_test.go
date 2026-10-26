package luxir

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

type fakeSearcher struct {
	luxirpb.UnimplementedSearcherServer
	reply func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error)

	mu   sync.Mutex
	reqs []*luxirpb.SearchRequest
}

func (f *fakeSearcher) Search(stream grpc.BidiStreamingServer[luxirpb.SearchRequest, luxirpb.SearchResponse]) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()

	resps, err := f.reply(req)
	for _, r := range resps {
		if sendErr := stream.Send(r); sendErr != nil {
			return sendErr
		}
	}
	return err
}

func (f *fakeSearcher) last(t *testing.T) *luxirpb.SearchRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("fake Searcher got no request")
	}
	return f.reqs[len(f.reqs)-1]
}

func (f *fakeSearcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func fakeLuxir(t *testing.T, reply func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error)) (*Luxir, *fakeSearcher) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSearcher{reply: reply}
	srv := grpc.NewServer()
	luxirpb.RegisterSearcherServer(srv, f)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	l, err := NewLuxir(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.conn.Close() })
	return l, f
}

type updateStream = grpc.BidiStreamingServer[luxirpb.UpdateRequest, luxirpb.UpdateResponse]

type fakeIndexer struct {
	luxirpb.UnimplementedIndexerServer
	stream func(updateStream) error
}

func (f *fakeIndexer) UpdateStream(s updateStream) error { return f.stream(s) }

func fakeServer(t *testing.T, register func(*grpc.Server), opts ...ClientOption) *Luxir {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	register(srv)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	l, err := NewLuxir(lis.Addr().String(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.conn.Close() })
	return l
}

type fakeAdmin struct {
	luxirpb.UnimplementedAdminServer
	create error
}

func (f *fakeAdmin) CreateCollection(_ context.Context, r *luxirpb.CreateCollectionRequest) (*luxirpb.CreateCollectionResponse, error) {
	return &luxirpb.CreateCollectionResponse{Name: r.GetName()}, f.create
}

func fakeIndexerLuxir(t *testing.T, stream func(updateStream) error) *Luxir {
	t.Helper()
	return fakeIndexerAdminLuxir(t, stream, nil)
}

func fakeIndexerAdminLuxir(t *testing.T, stream func(updateStream) error, create error) *Luxir {
	t.Helper()
	return fakeServer(t, func(s *grpc.Server) {
		luxirpb.RegisterIndexerServer(s, &fakeIndexer{stream: stream})
		luxirpb.RegisterAdminServer(s, &fakeAdmin{create: create})
	})
}

func fakeReply(resps ...*luxirpb.SearchResponse) func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error) {
	return func(*luxirpb.SearchRequest) ([]*luxirpb.SearchResponse, error) { return resps, nil }
}

func parseText[M proto.Message](t *testing.T, text string, m M) M {
	t.Helper()
	if err := prototext.Unmarshal([]byte(text), m); err != nil {
		t.Fatalf("bad text proto: %v\n%s", err, text)
	}
	return m
}

func wantQuery(t *testing.T, text string) *luxirpb.Query {
	return parseText(t, text, &luxirpb.Query{})
}

func wantRequest(t *testing.T, text string) *luxirpb.SearchRequest {
	return parseText(t, text, &luxirpb.SearchRequest{})
}

func wantResponse(t *testing.T, text string) *luxirpb.SearchResponse {
	return parseText(t, text, &luxirpb.SearchResponse{})
}

func queryProto(t *testing.T, q Query) *luxirpb.Query {
	t.Helper()
	pq, err := q.toProto()
	if err != nil {
		t.Fatalf("toProto: %v", err)
	}
	return pq
}

func assertProto(t *testing.T, got, want proto.Message) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Errorf("\n got:  %s\n want: %s", prototext.Format(got), prototext.Format(want))
	}
}

func searchRequest(t *testing.T, opts ...SearchOption) *luxirpb.SearchRequest {
	t.Helper()
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	if _, err := l.Search(timeout(t), "c", nil, opts...); err != nil {
		t.Fatalf("Search: %v", err)
	}
	return f.last(t)
}
