package luxir

import (
	"context"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"iter"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

const (
	streamBatchDocs  = 1000
	streamBatchBytes = 3 << 20 // ;; leaves room under Luxir's 4 MiB message limit
	streamLanes      = 4       // ;; Luxir applies one stream's batches in order, parallel streams index in parallel
	commitRequestID  = "commit"
)

// IndexStream ;; index any number of documents over parallel streams, sent in batches under the 4 MiB limit
//
// ;; collection "" defaults to "main", pass a slice as slices.Values(docs)
// ;; documents with the same id always go down the same stream, so the last one wins like with Index
// ;; commit options commit once, after every batch is in
// ;; WithAllOrNone holds per batch, Luxir can't roll back across messages
// ;; a document that can't be converted stops the stream, nothing after it is sent or committed
// ;; the response is every batch merged, Errors[].Index is the position in docs and Ids are in docs order
// ;; returns *IndexError if some or all the documents fail, with the merged response so far
func (l *Luxir) IndexStream(ctx context.Context, collection string, docs iter.Seq[Document], opts ...UpdateOption) (*luxirpb.UpdateResponse, error) {
	tmpl := &luxirpb.UpdateRequest{Collection: collection}
	for _, o := range opts {
		if o != nil {
			o(tmpl)
		}
	}
	commit := tmpl.Commit
	tmpl.Commit = nil

	// ;; a field map can rename another key onto id, so routing by "id" could split one id across streams
	n := streamLanes
	if len(tmpl.FieldMap) > 0 || tmpl.DropUnmapped {
		n = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newStreamMerge()
	lanes := make([]*streamLane, n)
	for i := range lanes {
		st, err := l.indexer.UpdateStream(ctx)
		if err != nil {
			return nil, &IndexError{Err: err}
		}
		lanes[i] = &streamLane{stream: st, batches: make(chan *luxirpb.UpdateRequest, 2)}
		go lanes[i].recv(m)
	}
	var senders sync.WaitGroup
	for _, ln := range lanes {
		senders.Add(1)
		go func() {
			defer senders.Done()
			ln.send(m)
		}()
	}

	var (
		pos     int
		rr      int
		first   = true
		convErr *IndexError
	)
	enqueue := func(ln *streamLane) {
		if ln.batch == nil {
			return
		}
		m.enqueue(ln.batch, ln.positions)
		select {
		case ln.batches <- ln.batch:
		case <-ctx.Done():
		}
		ln.batch, ln.positions, ln.size = nil, nil, 0
	}
	if docs != nil {
		for d := range docs {
			if m.stopped() {
				break
			}
			pm, err := d.toMap()
			if err != nil {
				convErr = &IndexError{Msg: fmt.Sprintf("err on document: %d: %v", pos, err), Err: err}
				break
			}
			// ;; parallel writes into a collection a write just auto-created can hang in Luxir,
			// ;; one made with CreateCollection can't, so make it before routing anything (already there is fine)
			if first {
				first = false
				if n > 1 && !l.ensureCollection(ctx, collection) {
					n = 1
				}
			}
			ln := lanes[laneOf(d, n, &rr)]
			size := mapBound(pm)
			if ln.batch != nil && (len(ln.batch.Docs) >= streamBatchDocs || ln.size+size > streamBatchBytes) {
				enqueue(ln)
			}
			if ln.batch == nil {
				ln.batch = proto.CloneOf(tmpl)
			}
			ln.batch.Docs = append(ln.batch.Docs, pm)
			ln.positions = append(ln.positions, pos)
			ln.size += size
			pos++
		}
	}
	for _, ln := range lanes {
		if convErr == nil && !m.stopped() {
			enqueue(ln)
		}
		close(ln.batches)
	}
	senders.Wait()
	m.wait()

	if commit != nil && convErr == nil && !m.stopped() {
		m.enqueue(nil, nil)
		lanes[0].sendOne(m, &luxirpb.UpdateRequest{RequestId: commitRequestID, Collection: collection, Commit: commit})
		m.wait()
	}
	if m.failed() == nil {
		for _, ln := range lanes {
			ln.stream.CloseSend()
		}
	}

	resp := m.result()
	switch err := m.failed(); {
	case convErr != nil:
		convErr.Resp = resp
		return resp, convErr
	case err != nil:
		return resp, &IndexError{Resp: resp, Msg: fmt.Sprintf("stream: %v", err), Err: err}
	case resp.GetStatus() != luxirpb.UpdateResponse_OK:
		return resp, &IndexError{Resp: resp}
	}
	return resp, nil
}

// ensureCollection ;; make the collection if it isn't there, false if that couldn't be checked
func (l *Luxir) ensureCollection(ctx context.Context, name string) bool {
	if name == "" {
		name = "main"
	}
	_, err := l.admin.CreateCollection(ctx, &luxirpb.CreateCollectionRequest{Name: name})
	return err == nil || status.Code(err) == codes.AlreadyExists
}

// laneOf ;; which stream a document goes down, by its id so every version of an id stays in order
func laneOf(d Document, n int, rr *int) int {
	if n == 1 {
		return 0
	}
	h, ok := idHash(d["id"])
	if !ok {
		*rr++ // ;; no id, nothing can replace it, so any stream will do
		return *rr % n
	}
	return int(h % uint64(n))
}

var laneSeed = maphash.MakeSeed()

// idPrefix ;; with long_terms truncate Luxir merges ids that share their first 255 bytes backed off to a
// ;; UTF-8 boundary (at least 252), so only that much of an id can decide its stream
const idPrefix = 252

// idHash ;; equal ids in Luxir's eyes always hash the same, different ids may share a hash (that only costs balance)
//
// ;; Luxir makes a numeric id a string (1000030.0, 1000030 and "1000030" are one id) with its own float
// ;; formatting, so every number, and every string that reads as one, hashes by its value instead of its text
func idHash(id any) (uint64, bool) {
	switch id := id.(type) {
	case nil:
		return 0, false
	case string:
		return strHash(id), true
	case []byte:
		return strHash(string(id)), true
	case bool:
		return strHash(strconv.FormatBool(id)), true
	case int:
		return numHash(float64(id)), true
	case int8:
		return numHash(float64(id)), true
	case int16:
		return numHash(float64(id)), true
	case int32:
		return numHash(float64(id)), true
	case int64:
		return numHash(float64(id)), true
	case uint8:
		return numHash(float64(id)), true
	case uint16:
		return numHash(float64(id)), true
	case uint32:
		return numHash(float64(id)), true
	case float32:
		// ;; Luxir writes a float32 with float32 digits, 1.1 not 1.100000023841858, read those digits as a float64
		return strHash(strconv.FormatFloat(float64(id), 'g', -1, 32)), true
	case float64:
		return numHash(id), true
	case time.Time:
		return numHash(float64(id.UnixMilli())), true
	}
	return strHash(fmt.Sprint(id)), true
}

func strHash(s string) uint64 {
	if len(s) > idPrefix {
		s = s[:idPrefix]
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return numHash(f)
	}
	return maphash.String(laneSeed, s)
}

func numHash(f float64) uint64 {
	switch {
	case f == 0:
		f = 0 // ;; -0 too
	case math.IsNaN(f):
		f = math.NaN()
	}
	return maphash.Comparable(laneSeed, math.Float64bits(f))
}

// streamLane ;; one update stream, its next batch, and the goroutines feeding and reading it
type streamLane struct {
	stream    luxirpb.Indexer_UpdateStreamClient
	batches   chan *luxirpb.UpdateRequest
	batch     *luxirpb.UpdateRequest
	positions []int
	size      int
}

func (ln *streamLane) send(m *streamMerge) {
	for r := range ln.batches {
		ln.sendOne(m, r)
	}
}

func (ln *streamLane) sendOne(m *streamMerge, r *luxirpb.UpdateRequest) {
	if m.stopped() {
		m.skip()
		return
	}
	// ;; io.EOF means Luxir ended the stream, its status comes from Recv
	if err := ln.stream.Send(r); err != nil && !errors.Is(err, io.EOF) {
		m.fail(err)
	}
}

func (ln *streamLane) recv(m *streamMerge) {
	for {
		resp, err := ln.stream.Recv()
		m.add(streamAck{resp, err})
		if err != nil {
			return
		}
	}
}

// streamAck ;; one Recv from an update stream
type streamAck struct {
	resp *luxirpb.UpdateResponse
	err  error
}

// streamMerge ;; the batch responses of one IndexStream folded into one, shared by every lane
type streamMerge struct {
	mu      sync.Mutex
	done    *sync.Cond
	next    int           // ;; request id of the next batch
	pending int           // ;; batches sent or queued and not answered yet
	batches map[int][]int // ;; position in docs of each document of each unanswered batch
	ids     []posID
	errs    []*luxirpb.UpdateResponse_DocError
	total   int64
	ver     uint64
	reqErr  *luxirpb.Error

	ok, partial, failedN int
	commitFailed         bool
	err                  error // ;; a stream itself failed
}

type posID struct {
	pos int
	id  string
}

func newStreamMerge() *streamMerge {
	m := &streamMerge{batches: map[int][]int{}}
	m.done = sync.NewCond(&m.mu)
	return m
}

// enqueue ;; number the batch and count it as pending, a nil batch is the commit
func (m *streamMerge) enqueue(r *luxirpb.UpdateRequest, positions []int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending++
	if r != nil {
		r.RequestId = strconv.Itoa(m.next)
		m.batches[m.next] = positions
		m.next++
	}
}

// skip ;; a queued batch that won't be sent after all
func (m *streamMerge) skip() {
	m.mu.Lock()
	m.pending--
	m.mu.Unlock()
	m.done.Broadcast()
}

func (m *streamMerge) fail(err error) {
	m.mu.Lock()
	if m.err == nil {
		m.err = err
	}
	m.mu.Unlock()
	m.done.Broadcast()
}

func (m *streamMerge) failed() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// stopped ;; nothing more should be sent
func (m *streamMerge) stopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err != nil || m.reqErr != nil
}

// wait ;; until every pending batch is answered or a stream fails
func (m *streamMerge) wait() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.pending > 0 && m.err == nil {
		m.done.Wait()
	}
}

func (m *streamMerge) add(a streamAck) {
	defer m.done.Broadcast()
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.err != nil {
		if errors.Is(a.err, io.EOF) {
			if m.pending == 0 {
				return // ;; the stream closed after answering everything
			}
			a.err = io.ErrUnexpectedEOF // ;; Luxir answers every request, an early end is a failure
		}
		if m.err == nil {
			m.err = a.err
		}
		return
	}
	r := a.resp
	m.pending--
	m.ver = max(m.ver, r.GetUpdateVersion())
	if r.GetError() != nil && m.reqErr == nil {
		m.reqErr = r.GetError()
	}
	// ;; the commit has no documents, it only counts if it failed
	if r.GetRequestId() == commitRequestID {
		m.commitFailed = r.GetStatus() != luxirpb.UpdateResponse_OK
		return
	}
	switch r.GetStatus() {
	case luxirpb.UpdateResponse_OK:
		m.ok++
	case luxirpb.UpdateResponse_PARTIAL:
		m.partial++
	default:
		m.failedN++
	}

	b, err := strconv.Atoi(r.GetRequestId())
	positions, known := m.batches[b]
	if err != nil || !known {
		positions = nil
	}
	delete(m.batches, b)
	at := func(i int32) int32 {
		if i >= 0 && int(i) < len(positions) {
			return int32(positions[i])
		}
		return i
	}
	bad := map[int32]bool{}
	for _, e := range r.GetErrors() {
		bad[e.GetIndex()] = true
		e = proto.CloneOf(e)
		e.Index = at(e.Index)
		m.errs = append(m.errs, e)
	}
	m.total += r.GetTotalErrors()
	// ;; ids line up with the batch's documents once the failed ones are skipped
	i := int32(0)
	for _, id := range r.GetIds() {
		for bad[i] {
			i++
		}
		m.ids = append(m.ids, posID{int(at(i)), id})
		i++
	}
}

func (m *streamMerge) result() *luxirpb.UpdateResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	slices.SortStableFunc(m.ids, func(a, b posID) int { return a.pos - b.pos })
	slices.SortStableFunc(m.errs, func(a, b *luxirpb.UpdateResponse_DocError) int { return int(a.GetIndex() - b.GetIndex()) })
	out := &luxirpb.UpdateResponse{UpdateVersion: m.ver, Errors: m.errs, TotalErrors: m.total, Error: m.reqErr}
	for _, p := range m.ids {
		out.Ids = append(out.Ids, p.id)
	}
	switch {
	case m.reqErr != nil || m.err != nil || m.commitFailed:
		out.Status = luxirpb.UpdateResponse_ERROR
	case m.partial == 0 && m.failedN == 0:
		out.Status = luxirpb.UpdateResponse_OK
	case m.ok == 0 && m.partial == 0:
		out.Status = luxirpb.UpdateResponse_ERROR
	default:
		out.Status = luxirpb.UpdateResponse_PARTIAL
	}
	return out
}

// mapBound ;; an upper bound on a document's encoded size, much cheaper than proto.Size
//
// ;; every varint and length prefix is counted at its widest, so a batch never goes over the limit
func mapBound(m *luxirpb.Map) int {
	n := 0
	for k, v := range m.GetFields() {
		n += len(k) + valBound(v) + 16
	}
	return n
}

func valBound(v *luxirpb.Val) int {
	const head = 16
	switch x := v.GetKind().(type) {
	case *luxirpb.Val_S:
		return len(x.S) + head
	case *luxirpb.Val_Bin:
		return len(x.Bin) + head
	case *luxirpb.Val_ArrS:
		n := head
		for _, s := range x.ArrS.GetV() {
			n += len(s) + 6
		}
		return n
	case *luxirpb.Val_ArrBin:
		n := head
		for _, b := range x.ArrBin.GetV() {
			n += len(b) + 6
		}
		return n
	case *luxirpb.Val_ArrI:
		return 11*len(x.ArrI.GetV()) + head
	case *luxirpb.Val_ArrF:
		return 5*len(x.ArrF.GetV()) + head
	case *luxirpb.Val_ArrD:
		return 9*len(x.ArrD.GetV()) + head
	case *luxirpb.Val_Vec:
		return 5*len(x.Vec.GetF32().GetV()) + 2*head
	case *luxirpb.Val_ArrVec:
		n := head
		for _, vec := range x.ArrVec.GetV() {
			n += 5*len(vec.GetF32().GetV()) + 2*head
		}
		return n
	case *luxirpb.Val_Arr:
		n := head
		for _, e := range x.Arr.GetV() {
			n += valBound(e) + 6
		}
		return n
	case *luxirpb.Val_Map:
		return mapBound(x.Map) + head
	default:
		return head + 11
	}
}
