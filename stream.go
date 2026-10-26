package luxir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"strconv"

	"google.golang.org/protobuf/proto"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

const (
	streamBatchDocs  = 1000
	streamBatchBytes = 3 << 20 // ;; leaves room under Luxir's 4 MiB message limit
	commitRequestID  = "commit"
)

// IndexStream ;; index any number of documents over one stream, sent in batches under the 4 MiB limit
//
// ;; collection "" defaults to "main", pass a slice as slices.Values(docs)
// ;; commit options commit once, after every batch is in
// ;; WithAllOrNone holds per batch, Luxir can't roll back across messages
// ;; a document that can't be converted stops the stream, nothing after it is sent or committed
// ;; the response is every batch merged, Errors[].Index is the position in docs
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := l.indexer.UpdateStream(ctx)
	if err != nil {
		return nil, &IndexError{Err: err}
	}

	acks := make(chan streamAck)
	go func() {
		for {
			resp, err := stream.Recv()
			select {
			case acks <- streamAck{resp, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	m := &streamMerge{}
	pending := 0
	take := func(a streamAck) {
		m.add(a)
		if a.err == nil {
			pending--
		}
	}
	// poll ;; take acks that are ready, so a failed request stops the stream early
	poll := func() {
		for {
			select {
			case a := <-acks:
				take(a)
			default:
				return
			}
		}
	}
	wait := func() {
		for pending > 0 && m.err == nil {
			select {
			case a := <-acks:
				take(a)
			case <-ctx.Done():
				m.err = ctx.Err()
			}
		}
	}
	send := func(r *luxirpb.UpdateRequest) {
		if err := stream.Send(r); err != nil {
			// ;; io.EOF means Luxir ended the stream, its status comes from Recv
			if !errors.Is(err, io.EOF) {
				m.fail(err)
			}
			pending++ // ;; never answered, wait stops on the stream error
			return
		}
		pending++
	}

	var (
		batch   *luxirpb.UpdateRequest
		size    int
		pos     int
		convErr *IndexError
	)
	flush := func() {
		if batch != nil {
			send(batch)
			batch = nil
		}
	}
	if docs != nil {
		for d := range docs {
			pm, err := d.toMap()
			if err != nil {
				convErr = &IndexError{Msg: fmt.Sprintf("err on document: %d: %v", pos, err), Err: err}
				break
			}
			n := proto.Size(pm)
			if batch != nil && (len(batch.Docs) >= streamBatchDocs || size+n > streamBatchBytes) {
				flush()
			}
			if batch == nil {
				batch = proto.CloneOf(tmpl)
				batch.RequestId = strconv.Itoa(len(m.starts))
				m.starts = append(m.starts, pos)
				size = 0
			}
			batch.Docs = append(batch.Docs, pm)
			size += n
			pos++

			poll()
			if m.stopped() {
				batch = nil
				break
			}
		}
	}
	if convErr == nil && !m.stopped() {
		flush()
	}
	wait()

	if commit != nil && convErr == nil && !m.stopped() {
		send(&luxirpb.UpdateRequest{RequestId: commitRequestID, Collection: collection, Commit: commit})
		wait()
	}
	if m.err == nil {
		stream.CloseSend()
	}

	resp := m.result()
	switch {
	case convErr != nil:
		convErr.Resp = resp
		return resp, convErr
	case m.err != nil:
		return resp, &IndexError{Resp: resp, Msg: fmt.Sprintf("stream: %v", m.err), Err: m.err}
	case resp.GetStatus() != luxirpb.UpdateResponse_OK:
		return resp, &IndexError{Resp: resp}
	}
	return resp, nil
}

// streamAck ;; one Recv from the update stream
type streamAck struct {
	resp *luxirpb.UpdateResponse
	err  error
}

// streamMerge ;; the batch responses of one IndexStream folded into one
type streamMerge struct {
	starts []int // ;; position in docs of each batch's first document
	ids    map[int][]string
	errs   []*luxirpb.UpdateResponse_DocError
	total  int64
	ver    uint64
	reqErr *luxirpb.Error

	ok, partial, failed int
	err                 error // ;; the stream itself failed
}

func (m *streamMerge) fail(err error) {
	if m.err == nil {
		m.err = err
	}
}

// stopped ;; nothing more should be sent
func (m *streamMerge) stopped() bool { return m.err != nil || m.reqErr != nil }

func (m *streamMerge) add(a streamAck) {
	if a.err != nil {
		if errors.Is(a.err, io.EOF) {
			a.err = io.ErrUnexpectedEOF // ;; Luxir answers every request, an early end is a failure
		}
		m.fail(a.err)
		return
	}
	r := a.resp
	m.ver = max(m.ver, r.GetUpdateVersion())
	if r.GetError() != nil && m.reqErr == nil {
		m.reqErr = r.GetError()
	}
	switch r.GetStatus() {
	case luxirpb.UpdateResponse_OK:
		m.ok++
	case luxirpb.UpdateResponse_PARTIAL:
		m.partial++
	default:
		m.failed++
	}
	if r.GetRequestId() == commitRequestID {
		return
	}

	start := 0
	b, err := strconv.Atoi(r.GetRequestId())
	if err == nil && b >= 0 && b < len(m.starts) {
		start = m.starts[b]
		if len(r.GetIds()) > 0 {
			if m.ids == nil {
				m.ids = map[int][]string{}
			}
			m.ids[b] = r.GetIds()
		}
	}
	for _, e := range r.GetErrors() {
		e = proto.CloneOf(e)
		e.Index += int32(start)
		m.errs = append(m.errs, e)
	}
	m.total += r.GetTotalErrors()
}

func (m *streamMerge) result() *luxirpb.UpdateResponse {
	out := &luxirpb.UpdateResponse{UpdateVersion: m.ver, Errors: m.errs, TotalErrors: m.total, Error: m.reqErr}
	for b := range m.starts {
		out.Ids = append(out.Ids, m.ids[b]...)
	}
	switch {
	case m.reqErr != nil || m.err != nil:
		out.Status = luxirpb.UpdateResponse_ERROR
	case m.partial == 0 && m.failed == 0:
		out.Status = luxirpb.UpdateResponse_OK
	case m.ok == 0 && m.partial == 0:
		out.Status = luxirpb.UpdateResponse_ERROR
	default:
		out.Status = luxirpb.UpdateResponse_PARTIAL
	}
	return out
}
