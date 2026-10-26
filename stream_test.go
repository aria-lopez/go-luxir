package luxir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

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

func TestIndexStream_AllFailWithCommit(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	docs := make([]Document, 1500)
	for i := range docs {
		docs[i] = Document{"id": fmt.Sprintf("bad%d", i), "nosuffix": i}
	}

	resp, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithCommit())

	var ie *IndexError
	if !errors.As(err, &ie) || resp.GetStatus() != luxirpb.UpdateResponse_ERROR || resp.GetTotalErrors() != 1500 {
		t.Errorf("status = %s total errors %d err %v, want ERROR for 1500 failures", resp.GetStatus(), resp.GetTotalErrors(), err)
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
		t.Fatalf("err = %v, want PARTIAL: the bad document's batch rolled back, the others kept", err)
	}
	if n := liveDocs(t, l, coll); n == 0 || n >= 1499 {
		t.Errorf("live docs = %d, want every batch but the bad one's", n)
	}
	r, err := l.Search(timeout(t), coll, nil, WithLimit(-1), WithFields("id"))
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(r.Hits)) != liveDocs(t, l, coll) {
		t.Errorf("search sees %d docs, stats %d", len(r.Hits), liveDocs(t, l, coll))
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
	st, err := l.Stats(timeout(t), coll, false)
	if err == nil && st.GetCollections()[0].GetTotals().GetLiveDocs() != 0 {
		t.Errorf("live docs = %d, want nothing committed", st.GetCollections()[0].GetTotals().GetLiveDocs())
	} else if err != nil && !errors.Is(err, ErrCollectionNotFound) {
		t.Errorf("Stats: %v", err)
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
	var streams atomic.Int32
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		streams.Add(1)
		var mu sync.Mutex
		var wg sync.WaitGroup
		defer wg.Wait()
		for {
			r, err := s.Recv()
			if err != nil {
				return nil
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(rand.IntN(3000)) * time.Microsecond)
				resp := &luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_OK, UpdateVersion: 9}
				if r.GetCommit() == nil {
					resp.Status = luxirpb.UpdateResponse_PARTIAL
					resp.UpdateVersion = 1
					for i, d := range r.GetDocs() {
						id := d.GetFields()["id"].GetS()
						if i == 1 {
							resp.Errors = append(resp.Errors, &luxirpb.UpdateResponse_DocError{Id: id, Index: 1})
							resp.TotalErrors++
							continue
						}
						resp.Ids = append(resp.Ids, id)
					}
				}
				mu.Lock()
				defer mu.Unlock()
				s.Send(resp)
			}()
		}
	})
	docs := numbered(9000)

	resp, err := l.IndexStream(timeout(t), "c", slices.Values(docs), WithCommit(), WithReturnIDs())

	var ie *IndexError
	if !errors.As(err, &ie) || !ie.IsPartial() {
		t.Fatalf("err = %v, want PARTIAL", err)
	}
	if streams.Load() != streamLanes {
		t.Errorf("opened %d streams, want %d", streams.Load(), streamLanes)
	}
	failed := map[int]bool{}
	for i, e := range resp.GetErrors() {
		if docs[e.GetIndex()]["id"] != e.GetId() {
			t.Fatalf("error %d at position %d names %q, but that document is %v", i, e.GetIndex(), e.GetId(), docs[e.GetIndex()]["id"])
		}
		if i > 0 && e.GetIndex() <= resp.GetErrors()[i-1].GetIndex() {
			t.Errorf("errors not in document order at %d", i)
		}
		failed[int(e.GetIndex())] = true
	}
	var want []string
	for i, d := range docs {
		if !failed[i] {
			want = append(want, d["id"].(string))
		}
	}
	if !reflect.DeepEqual(resp.GetIds(), want) {
		t.Errorf("ids = %d, want the %d successful ids in document order", len(resp.GetIds()), len(want))
	}
	if int64(len(failed)) != resp.GetTotalErrors() || len(failed) < 9 || resp.GetUpdateVersion() != 9 {
		t.Errorf("%d failed, total %d, version %d", len(failed), resp.GetTotalErrors(), resp.GetUpdateVersion())
	}
}

func TestIndexStream_WhenItGoesParallel(t *testing.T) {
	tests := []struct {
		name   string
		opts   []UpdateOption
		create error
		want   int32
	}{
		{"plain", nil, nil, streamLanes},
		{"collection already there", nil, status.Error(codes.AlreadyExists, "exists"), streamLanes},
		{"other settings", []UpdateOption{WithAllOrNone(), WithReturnIDs()}, nil, streamLanes},
		{"field map", []UpdateOption{WithFieldMap(map[string]string{"key": "id"})}, nil, 1},
		{"drop unmapped", []UpdateOption{WithDropUnmapped()}, nil, 1},
		{"can't make the collection", nil, status.Error(codes.FailedPrecondition, "read only"), 1},
		{"no admin service", nil, status.Error(codes.Unimplemented, "nope"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var used atomic.Int32
			l := fakeIndexerAdminLuxir(t, func(s updateStream) error {
				counted := false
				for {
					r, err := s.Recv()
					if err != nil {
						return nil
					}
					if !counted && r.GetCommit() == nil {
						counted = true
						used.Add(1)
					}
					s.Send(&luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_OK})
				}
			}, tt.create)
			if _, err := l.IndexStream(timeout(t), "c", slices.Values(numbered(400)), tt.opts...); err != nil {
				t.Fatal(err)
			}
			if used.Load() != tt.want {
				t.Errorf("documents went down %d streams, want %d", used.Load(), tt.want)
			}
		})
	}
}

func TestIndexStream_NothingToSendMakesNothing(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.IndexStream(timeout(t), coll, slices.Values([]Document{})); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Stats(timeout(t), coll, false); !errors.Is(err, ErrCollectionNotFound) {
		t.Errorf("Stats = %v, want the collection never made by an empty stream", err)
	}
}

func TestLaneOf(t *testing.T) {
	rr := 0
	same := []Document{{"id": "123"}, {"id": 123}, {"id": int64(123)}, {"id": int32(123)}, {"id": uint16(123)}, {"id": []byte("123")}}
	for _, d := range same {
		if got, want := laneOf(d, streamLanes, &rr), laneOf(same[0], streamLanes, &rr); got != want {
			t.Errorf("laneOf(%#v) = %d, want %d: the same id must take the same stream", d["id"], got, want)
		}
	}
	hit := map[int]int{}
	for i := range 4000 {
		hit[laneOf(Document{"id": fmt.Sprintf("doc-%d", i)}, streamLanes, &rr)]++
		hit[laneOf(Document{"t_s": "no id"}, streamLanes, &rr)]++
	}
	for lane := range streamLanes {
		if hit[lane] < 1500 {
			t.Errorf("lane %d got %d of 8000 documents, want them spread out", lane, hit[lane])
		}
	}
	if laneOf(Document{"id": "x"}, 1, &rr) != 0 {
		t.Error("one lane must always be lane 0")
	}
}

func TestIndexStream_SameIDLastWins(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	docs := make([]Document, 30000)
	for i := range docs {
		var id any = fmt.Sprintf("k%d", i%500)
		if i%500 == 7 && i%1000 < 500 {
			id = 7000 + i%3
		}
		docs[i] = Document{"id": id, "n_i": i}
	}
	docs[len(docs)-1] = Document{"id": "k499", "n_i": -1}

	if _, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithCommit()); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}

	last := map[string]int64{}
	for _, d := range docs {
		last[fmt.Sprint(d["id"])] = int64(d["n_i"].(int))
	}
	r, err := l.Search(timeout(t), coll, nil, WithLimit(-1), WithFields("id", "n_i"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != len(last) {
		t.Errorf("got %d docs, want %d distinct ids", len(r.Hits), len(last))
	}
	for _, h := range r.Hits {
		if id := fmt.Sprint(h.Doc["id"]); h.Doc["n_i"] != last[id] {
			t.Errorf("id %s has n_i %v, want the last version %d", id, h.Doc["n_i"], last[id])
		}
	}
}

func TestIndexStream_ManyLargeIDs(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	pad := strings.Repeat("p", 200)
	docs := make([]Document, 120000)
	for i := range docs {
		docs[i] = Document{"id": fmt.Sprintf("%s%07d", pad, i)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resp, err := l.IndexStream(ctx, coll, slices.Values(docs), WithReturnIDs(), WithCommit())
	if err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	if len(resp.GetIds()) != len(docs) || resp.GetIds()[0] != docs[0]["id"] || resp.GetIds()[len(docs)-1] != docs[len(docs)-1]["id"] {
		t.Errorf("got %d ids, want all %d in order", len(resp.GetIds()), len(docs))
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
	commit := func(s luxirpb.UpdateResponse_Status) streamAck {
		return streamAck{resp: &luxirpb.UpdateResponse{RequestId: commitRequestID, Status: s}}
	}

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
		{"all failed then commit ok", []streamAck{bad, bad, commit(luxirpb.UpdateResponse_OK)}, luxirpb.UpdateResponse_ERROR},
		{"all ok then commit ok", []streamAck{ok, commit(luxirpb.UpdateResponse_OK)}, luxirpb.UpdateResponse_OK},
		{"one failed then commit ok", []streamAck{ok, bad, commit(luxirpb.UpdateResponse_OK)}, luxirpb.UpdateResponse_PARTIAL},
		{"commit failed", []streamAck{ok, commit(luxirpb.UpdateResponse_ERROR)}, luxirpb.UpdateResponse_ERROR},
		{"only a commit", []streamAck{commit(luxirpb.UpdateResponse_OK)}, luxirpb.UpdateResponse_OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newStreamMerge()
			for _, a := range tt.acks {
				m.add(a)
			}
			if got := m.result().GetStatus(); got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMapBound_NeverUnder(t *testing.T) {
	docs := []Document{
		{},
		{"id": "a"},
		{"s": strings.Repeat("x", 300), "big": strings.Repeat("y", 70000), "empty": ""},
		{"i": int64(math.MinInt64), "j": int64(math.MaxInt64), "k": -1, "b": true, "n": nil, "f": float32(1.5), "d": 2.5},
		{"bin": []byte("raw"), "bins": [][]byte{[]byte("a"), make([]byte, 200)}},
		{"ss": []string{"", "a", strings.Repeat("z", 1000)}, "is": []int64{math.MinInt64, -1, 0, math.MaxInt64}, "fs": []float32{1, 2}, "ds": []float64{1, 2, 3}},
		{"vec": []float32{1, 2, 3, 4}, "mv": [][]float32{{1, 2}, {}, {3, 4, 5}}, "pt": Point{Lat: 1, Lon: 2}, "pts": []Point{{1, 2}, {3, 4}}},
		{"mixed": []any{"a", int64(-5), 2.5, []string{"x"}, []any{"deep", []any{int64(1)}}}, "when": time.Now()},
	}
	for i := 0; i < 50; i++ {
		d := Document{}
		for j := 0; j < 20; j++ {
			d[fmt.Sprintf("f%d_%s", j, strings.Repeat("k", i))] = []any{strings.Repeat("v", i*j), int64(-i * j), float64(j)}
		}
		docs = append(docs, d)
	}
	for i, d := range docs {
		m, err := d.toMap()
		if err != nil {
			t.Fatal(err)
		}
		if got, real := mapBound(m), proto.Size(m); got < real {
			t.Errorf("doc %d: bound %d is under the real size %d", i, got, real)
		}
	}
}

func TestIndexStream_NewCollectionsDontHang(t *testing.T) {
	l := getClient(t)
	ctx := context.Background()
	for i := range 1500 {
		coll := fmt.Sprintf("go_luxir_new_%d_%d", time.Now().UnixNano(), i)
		docs := numbered(2 + i%7)
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := l.IndexStream(c, coll, slices.Values(docs))
		cancel()
		l.DeleteCollection(ctx, coll)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

// TestLaneOf_EquivalentIDs ;; every spelling Luxir reads as one id takes one stream (Luxir writes 1000030.0 as "1000030")
func TestLaneOf_EquivalentIDs(t *testing.T) {
	long := strings.Repeat("é", 126) // ;; 252 bytes
	groups := [][]any{
		{float64(1000030), 1000030, int64(1000030), "1000030", float32(1000030), "1.00003e+06"},
		{float64(2000001), 2000001, "2000001"},
		{1.5, "1.5", float32(1.5)},
		{float32(1.1), "1.1"},
		{123456789.25, "123456789.25"},
		{1e21, "1e+21"},
		{1e-7, "1e-07"},
		{float64(1 << 62), int64(1 << 62), "4611686018427387904"},
		{0.0, math.Copysign(0, -1), 0, "0", "-0"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
		{math.NaN(), "nan", float32(math.NaN())},
		{true, "true"},
		{false, "false"},
		{time.UnixMilli(1700000000000), int64(1700000000000), "1700000000000"},
		{uint32(7), uint8(7), uint16(7), int8(7), int16(7), "7", []byte("7")},
		{"k1", []byte("k1")},
		// ;; long_terms truncate merges ids sharing 255 bytes backed off to a UTF-8 boundary
		{long + "a", long + "é", long + "aaaaaaaaaaaaa"},
	}
	for _, g := range groups {
		for _, n := range []int{2, 3, streamLanes, 16} {
			rr := 0
			want := laneOf(Document{"id": g[0]}, n, &rr)
			for _, id := range g[1:] {
				if got := laneOf(Document{"id": id}, n, &rr); got != want {
					t.Errorf("%d lanes: laneOf(%T %v) = %d, laneOf(%T %v) = %d, want one stream", n, id, id, got, g[0], g[0], want)
				}
			}
		}
	}
	// ;; an unsupported id never gets past toMap, it still has to route somewhere
	rr := 0
	if got := laneOf(Document{"id": struct{}{}}, streamLanes, &rr); got < 0 || got >= streamLanes {
		t.Errorf("laneOf(struct{}) = %d", got)
	}
}

// TestIndexStream_EquivalentIDsLastWins ;; float, int and string spellings of one id interleaved, the last one wins like with Index
func TestIndexStream_EquivalentIDsLastWins(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	var docs []Document
	for i := range 2000 {
		n := 1000001 + i
		docs = append(docs,
			Document{"id": float64(n), "n_i": 1},
			Document{"id": n, "n_i": 2},
			Document{"id": float64(n), "n_i": 3},
			Document{"id": strconv.Itoa(n), "n_i": 4})
	}
	if _, err := l.IndexStream(timeout(t), coll, slices.Values(docs), WithCommit()); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	r, err := l.Search(timeout(t), coll, nil, WithLimit(5), WithFields("id", "n_i"), WithFilter(Not(Match("n_i", 4))))
	if err != nil {
		t.Fatal(err)
	}
	all, err := l.Search(timeout(t), coll, nil, WithLimit(0))
	if err != nil {
		t.Fatal(err)
	}
	if r.Found != 0 || all.Found != 2000 {
		t.Errorf("%d of %d ids kept an older version, e.g. %v; want 0 of 2000", r.Found, all.Found, r.Hits)
	}
}

// TestIndexStream_ReusedBuffers ;; an iterator may reuse its slices once yield returns, nothing it yielded changes
func TestIndexStream_ReusedBuffers(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	ints, strs, raw := []int64{0}, []string{""}, []byte{0}
	seq := func(yield func(Document) bool) {
		for i := range 50 {
			ints[0], strs[0], raw[0] = int64(i), strconv.Itoa(i), byte(i)
			if !yield(Document{"id": strconv.Itoa(i), "n_is": ints, "s_ss": strs, "raw_s": raw}) {
				return
			}
		}
	}
	if _, err := l.IndexStream(timeout(t), coll, seq, WithCommit()); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	r, err := l.Search(timeout(t), coll, nil, WithLimit(-1))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 50 {
		t.Fatalf("got %d docs, want 50", len(r.Hits))
	}
	for _, h := range r.Hits {
		id := h.Doc["id"].(string)
		n, _ := strconv.Atoi(id)
		if fmt.Sprint(h.Doc["n_is"]) != "["+id+"]" || fmt.Sprint(h.Doc["s_ss"]) != "["+id+"]" || h.Doc["raw_s"] != string([]byte{byte(n)}) {
			t.Errorf("id %s = %v, want its own values, not the buffer's last", id, h.Doc)
		}
	}
}

// TestIndexStream_FakeDefaultCollection ;; "" is made as "main" before going parallel
func TestIndexStream_FakeDefaultCollection(t *testing.T) {
	var created atomic.Value
	l := fakeServer(t, func(s *grpc.Server) {
		luxirpb.RegisterIndexerServer(s, &fakeIndexer{stream: okStream})
		luxirpb.RegisterAdminServer(s, &recordAdmin{created: &created})
	})
	if _, err := l.IndexStream(timeout(t), "", slices.Values(numbered(10))); err != nil {
		t.Fatalf("IndexStream: %v", err)
	}
	if created.Load() != "main" {
		t.Errorf("created %v, want main", created.Load())
	}
}

type recordAdmin struct {
	luxirpb.UnimplementedAdminServer
	created *atomic.Value
}

func (a *recordAdmin) CreateCollection(_ context.Context, r *luxirpb.CreateCollectionRequest) (*luxirpb.CreateCollectionResponse, error) {
	a.created.Store(r.GetName())
	return &luxirpb.CreateCollectionResponse{}, nil
}

// okStream ;; answers every batch OK
func okStream(s updateStream) error {
	for {
		r, err := s.Recv()
		if err != nil {
			return nil
		}
		if err := s.Send(&luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_OK}); err != nil {
			return err
		}
	}
}

// TestIndexStream_UnsendableDocument ;; a batch gRPC can't encode (strings must be UTF-8) fails the call, nothing is committed
func TestIndexStream_UnsendableDocument(t *testing.T) {
	var commits atomic.Int32
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		for {
			r, err := s.Recv()
			if err != nil {
				return nil
			}
			if r.GetCommit() != nil {
				commits.Add(1)
			}
			s.Send(&luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_OK})
		}
	})
	docs := numbered(5000)
	docs[0]["t_s"] = "bad\xff"

	_, err := l.IndexStream(timeout(t), "c", slices.Values(docs), WithCommit())
	var ie *IndexError
	if !errors.As(err, &ie) || status.Code(ie.Err) != codes.Internal {
		t.Errorf("err = %v, want an *IndexError with the marshal failure", err)
	}
	if commits.Load() != 0 {
		t.Error("committed after a failed batch")
	}
}

// TestStreamLane_SkipsOnceStopped ;; a queued batch isn't sent after another lane failed, and stops counting as pending
func TestStreamLane_SkipsOnceStopped(t *testing.T) {
	m := newStreamMerge()
	m.enqueue(&luxirpb.UpdateRequest{}, []int{0})
	first := errors.New("first")
	m.fail(first)
	m.fail(errors.New("second"))
	(&streamLane{}).sendOne(m, &luxirpb.UpdateRequest{}) // ;; a nil stream would panic if it were used
	if m.pending != 0 || m.failed() != first {
		t.Errorf("pending, err = %d, %v, want 0 and the first failure", m.pending, m.failed())
	}
}

// TestStreamMerge_ErrorOutsideBatch ;; an error index Luxir sends outside the batch is passed through, not remapped
func TestStreamMerge_ErrorOutsideBatch(t *testing.T) {
	m := newStreamMerge()
	r := &luxirpb.UpdateRequest{}
	m.enqueue(r, []int{7})
	m.add(streamAck{resp: &luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_PARTIAL,
		Errors: []*luxirpb.UpdateResponse_DocError{{Index: 0}, {Index: 5}}}})
	if got := m.result().GetErrors(); len(got) != 2 || got[0].GetIndex() != 5 || got[1].GetIndex() != 7 {
		t.Errorf("errors = %v, want indexes 5 (passed through) and 7 (remapped)", got)
	}
}

// TestIndexStream_CancelledWhileBlocked ;; a server that stops reading can't hang the producer past the deadline
func TestIndexStream_CancelledWhileBlocked(t *testing.T) {
	l := fakeIndexerLuxir(t, func(s updateStream) error {
		<-s.Context().Done()
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := l.IndexStream(ctx, "c", slices.Values(bigDocs(40, 1<<20)), WithFieldMap(map[string]string{"body_s": "body_s"}))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("IndexStream = nil, want the deadline")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IndexStream still blocked 5s after its deadline")
	}
}

func TestValBound_NeverUnder(t *testing.T) {
	for _, v := range []*luxirpb.Val{
		{Kind: &luxirpb.Val_Vec{Vec: &luxirpb.Vector{Kind: &luxirpb.Vector_F32{F32: &luxirpb.ArrFloat{V: make([]float32, 100)}}}}},
		{Kind: &luxirpb.Val_Map{Map: &luxirpb.Map{Fields: map[string]*luxirpb.Val{"k": {Kind: &luxirpb.Val_S{S: strings.Repeat("x", 500)}}}}}},
	} {
		if got, real := valBound(v), proto.Size(v); got < real {
			t.Errorf("valBound(%T) = %d, under the real size %d", v.GetKind(), got, real)
		}
	}
}
