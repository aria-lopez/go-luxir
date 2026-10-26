package luxir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func numbered(n int) []Document {
	docs := make([]Document, n)
	for i := range docs {
		docs[i] = Document{"id": fmt.Sprintf("d%05d", i), "n_i": i}
	}
	return docs
}

func bigDocs(n, size int) []Document {
	body := strings.Repeat("x", size)
	docs := make([]Document, n)
	for i := range docs {
		docs[i] = Document{"id": fmt.Sprintf("big%d", i), "body_s": body}
	}
	return docs
}

func TestIndexStream_Many(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	docs := numbered(2500)

	resp, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithCommit(), WithReturnIDs())
	if err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	if resp.GetStatus() != luxirpb.UpdateResponse_OK {
		t.Errorf("status = %s, want OK", resp.GetStatus())
	}
	if got := liveDocs(t, l, coll); got != 2500 {
		t.Errorf("live docs = %d, want 2500", got)
	}
	want := make([]string, len(docs))
	for i, d := range docs {
		want[i] = d["id"].(string)
	}
	if !reflect.DeepEqual(resp.GetIds(), want) {
		t.Errorf("ids = %d of them, want all 2500 in document order", len(resp.GetIds()))
	}
}

func TestIndexStream_BiggerThanOneMessage(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.IndexStream(timeout(t), coll, slices.Values(bigDocs(12, 1<<20)), WithCommit()); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	if got := liveDocs(t, l, coll); got != 12 {
		t.Errorf("live docs = %d, want 12", got)
	}
}

func TestIndexStream_PartialPositionsAcrossBatches(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	docs := numbered(1800)
	docs[3] = Document{"id": "bad3", "nosuffix": 1}
	docs[1700] = Document{"id": "bad1700", "nosuffix": 1}

	resp, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || !ie.IsPartial() {
		t.Fatalf("err = %v, want a PARTIAL *IndexError", err)
	}
	var got []int32
	for _, f := range ie.Failed() {
		got = append(got, f.GetIndex())
	}
	if !reflect.DeepEqual(got, []int32{3, 1700}) || resp.GetTotalErrors() != 2 {
		t.Errorf("failed positions = %v (total %d), want [3 1700] as positions in docs", got, resp.GetTotalErrors())
	}
	if n := liveDocs(t, l, coll); n != 1798 {
		t.Errorf("live docs = %d, want 1798", n)
	}
}

func TestIndexStream_AllOrNoneIsPerBatch(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	docs := numbered(1500)
	docs[1200] = Document{"id": "bad", "nosuffix": 1}

	_, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithAllOrNone(), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || !ie.IsPartial() {
		t.Fatalf("err = %v, want PARTIAL: the first batch is kept, the second rolled back", err)
	}
	if n := liveDocs(t, l, coll); n != streamBatchDocs {
		t.Errorf("live docs = %d, want %d", n, streamBatchDocs)
	}
}

func TestIndexStream_ConvertErrorStopsWithoutCommit(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	docs := numbered(1500)
	docs[1200] = Document{"id": "bad", "x_s": struct{}{}}

	resp, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || !strings.Contains(ie.InternalError(), "document: 1200") {
		t.Fatalf("err = %v, want *IndexError naming document 1200", err)
	}
	if resp == nil || ie.Resp != resp {
		t.Errorf("resp = %v, want the merged response so far, also on the error", resp)
	}
	if n := liveDocs(t, l, coll); n != 0 {
		t.Errorf("live docs = %d, want nothing committed", n)
	}
}

func TestIndexStream_OversizedDocument(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	_, err := l.IndexStream(timeout(t), coll, slices.Values(bigDocs(1, 5<<20)))

	var ie *IndexError
	if !errors.As(err, &ie) || status.Code(err) != codes.ResourceExhausted {
		t.Errorf("err = %v (code %s), want *IndexError with ResourceExhausted", err, status.Code(err))
	}
}

func TestIndexStream_NothingToDo(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	for name, docs := range map[string]iter.Seq[Document]{"nil": nil, "empty": slices.Values([]Document{})} {
		resp, err := l.IndexStream(timeout(t), coll, docs)
		if err != nil || resp.GetStatus() != luxirpb.UpdateResponse_OK {
			t.Errorf("%s: resp = %v, err = %v, want OK", name, resp, err)
		}
	}
}

func TestIndexStream_OnlyCommit(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.Index(timeout(t), coll, []Document{{"id": "a", "t_s": "x"}}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if _, err := l.IndexStream(timeout(t), coll, nil, WithCommit()); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	if n := liveDocs(t, l, coll); n != 1 {
		t.Errorf("live docs = %d, want the earlier write committed", n)
	}
}

func TestIndexStream_Cancelled(t *testing.T) {
	l := getClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := l.IndexStream(ctx, "c", slices.Values(numbered(3)))

	if status.Code(err) != codes.Canceled {
		t.Errorf("err = %v, want Canceled", err)
	}
}

func TestIndexStream_StopIteratingEarly(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	pulled := 0
	docs := func(yield func(Document) bool) {
		for _, d := range numbered(1500) {
			pulled++
			if !yield(d) {
				return
			}
		}
	}
	docs1500 := iter.Seq[Document](docs)
	broken := func(yield func(Document) bool) {
		for d := range docs1500 {
			if d["n_i"] == 10 {
				d = Document{"id": "bad", "x_s": struct{}{}}
			}
			if !yield(d) {
				return
			}
		}
	}

	if _, err := l.IndexStream(timeout(t), coll, broken); err == nil {
		t.Fatal("IndexStream = nil error, want the conversion error")
	}
	if pulled != 11 {
		t.Errorf("pulled %d documents, want the iterator stopped right after the bad one", pulled)
	}
}

func TestIndexStream_FakeRequests(t *testing.T) {
	var mu sync.Mutex
	var reqs []*luxirpb.UpdateRequest
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		for {
			r, err := s.Recv()
			if err != nil {
				return nil
			}
			mu.Lock()
			reqs = append(reqs, r)
			mu.Unlock()
			if err := s.Send(&luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_OK}); err != nil {
				return err
			}
		}
	})

	_, err := l.IndexStream(timeout(t), "c", slices.Values(numbered(2100)),
		WithAllOrNone(), WithReturnIDs(), WithFieldMap(map[string]string{"a": "b"}), WithCommitWithin(5*time.Second), WithMaxSegments(3))
	if err != nil {
		t.Fatalf("IndexStream: %v", err)
	}

	if len(reqs) != 4 {
		t.Fatalf("got %d requests, want 3 batches and a commit", len(reqs))
	}
	for i, r := range reqs[:3] {
		if r.GetRequestId() != strconv.Itoa(i) || r.GetCollection() != "c" || !r.GetAllOrNone() || !r.GetReturnIds() || r.GetFieldMap()["a"] != "b" || r.GetCommit() != nil {
			t.Errorf("batch %d = id %q coll %q, want every option but commit on each batch", i, r.GetRequestId(), r.GetCollection())
		}
	}
	if n := []int{len(reqs[0].GetDocs()), len(reqs[1].GetDocs()), len(reqs[2].GetDocs())}; !reflect.DeepEqual(n, []int{1000, 1000, 100}) {
		t.Errorf("batch sizes = %v", n)
	}
	assertProto(t, reqs[3], parseText(t, `request_id:"commit" collection:"c" commit:{commit_within_ms:5000 max_segments:3}`, &luxirpb.UpdateRequest{}))
}

func TestIndexStream_FakeOutOfOrder(t *testing.T) {
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		var held []*luxirpb.UpdateRequest
		for {
			r, err := s.Recv()
			if err != nil {
				return nil
			}
			if r.GetRequestId() == commitRequestID {
				s.Send(&luxirpb.UpdateResponse{RequestId: commitRequestID, Status: luxirpb.UpdateResponse_OK, UpdateVersion: 9})
				continue
			}
			held = append(held, r)
			if len(held) < 2 {
				continue
			}
			for i := len(held) - 1; i >= 0; i-- {
				h := held[i]
				resp := &luxirpb.UpdateResponse{RequestId: h.GetRequestId(), Status: luxirpb.UpdateResponse_PARTIAL, UpdateVersion: uint64(i + 1),
					Ids: []string{"id" + h.GetRequestId()}, TotalErrors: 1,
					Errors: []*luxirpb.UpdateResponse_DocError{{Id: "x", Index: 1}}}
				s.Send(resp)
			}
		}
	})

	resp, err := l.IndexStream(timeout(t), "c", slices.Values(numbered(1500)), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || !ie.IsPartial() {
		t.Fatalf("err = %v, want PARTIAL", err)
	}
	if !reflect.DeepEqual(resp.GetIds(), []string{"id0", "id1"}) {
		t.Errorf("ids = %v, want batch order whatever the reply order", resp.GetIds())
	}
	var at []int32
	for _, e := range resp.GetErrors() {
		at = append(at, e.GetIndex())
	}
	slices.Sort(at)
	if !reflect.DeepEqual(at, []int32{1, 1001}) || resp.GetTotalErrors() != 2 || resp.GetUpdateVersion() != 9 {
		t.Errorf("errors at %v total %d version %d, want [1 1001], 2, 9", at, resp.GetTotalErrors(), resp.GetUpdateVersion())
	}
}

func TestIndexStream_FakeRequestFailure(t *testing.T) {
	var mu sync.Mutex
	commits := 0
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		for {
			r, err := s.Recv()
			if err != nil {
				return nil
			}
			mu.Lock()
			if r.GetCommit() != nil {
				commits++
			}
			mu.Unlock()
			s.Send(&luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_ERROR,
				Error: &luxirpb.Error{Kind: luxirpb.Error_UNAVAILABLE, Message: "disk full"}})
		}
	})

	resp, err := l.IndexStream(timeout(t), "c", slices.Values(numbered(3000)), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || ie.RequestFailed().GetMessage() != "disk full" || resp.GetStatus() != luxirpb.UpdateResponse_ERROR {
		t.Errorf("err = %v, resp = %v, want ERROR carrying the request failure", err, resp)
	}
	mu.Lock()
	defer mu.Unlock()
	if commits != 0 {
		t.Errorf("sent %d commits, want none after a failed request", commits)
	}
}

func TestIndexStream_FakeEndsEarly(t *testing.T) {
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		_, err := s.Recv()
		return err
	})

	_, err := l.IndexStream(timeout(t), "c", slices.Values(numbered(5)))

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want io.ErrUnexpectedEOF when Luxir stops answering", err)
	}
}

func TestIndexStream_FakeStatusError(t *testing.T) {
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		return status.Error(codes.NotFound, "no collection")
	})

	_, err := l.IndexStream(timeout(t), "c", slices.Values(numbered(5)))

	var ie *IndexError
	if !errors.As(err, &ie) || status.Code(err) != codes.NotFound {
		t.Errorf("err = %v, want *IndexError keeping the NotFound status", err)
	}
}

func TestStreamMerge_Status(t *testing.T) {
	ack := func(s luxirpb.UpdateResponse_Status) streamAck {
		return streamAck{resp: &luxirpb.UpdateResponse{Status: s}}
	}
	ok, partial, bad := ack(luxirpb.UpdateResponse_OK), ack(luxirpb.UpdateResponse_PARTIAL), ack(luxirpb.UpdateResponse_ERROR)

	tests := []struct {
		name string
		acks []streamAck
		want luxirpb.UpdateResponse_Status
	}{
		{"nothing", nil, luxirpb.UpdateResponse_OK},
		{"all ok", []streamAck{ok, ok}, luxirpb.UpdateResponse_OK},
		{"one partial", []streamAck{ok, partial}, luxirpb.UpdateResponse_PARTIAL},
		{"ok and error", []streamAck{ok, bad}, luxirpb.UpdateResponse_PARTIAL},
		{"all error", []streamAck{bad, bad}, luxirpb.UpdateResponse_ERROR},
		{"stream failed", []streamAck{ok, {err: errors.New("x")}}, luxirpb.UpdateResponse_ERROR},
		{"request failed", []streamAck{ok, {resp: &luxirpb.UpdateResponse{Status: luxirpb.UpdateResponse_ERROR, Error: &luxirpb.Error{}}}}, luxirpb.UpdateResponse_ERROR},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &streamMerge{}
			for _, a := range tt.acks {
				m.add(a)
			}
			if got := m.result().GetStatus(); got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
}
