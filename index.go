package luxir

import (
	"context"
	"fmt"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// UpdateOption ;; tweak option to an Index, IndexStream, Delete or Commit call
type UpdateOption func(*luxirpb.UpdateRequest)

// commitParams ;; the request's commit, made on first use so commit options stack
func commitParams(r *luxirpb.UpdateRequest) *luxirpb.CommitParams {
	if r.Commit == nil {
		r.Commit = &luxirpb.CommitParams{}
	}
	return r.Commit
}

// WithCommit ;; make the documents searchable before the Index returns
func WithCommit() UpdateOption {
	return func(r *luxirpb.UpdateRequest) { commitParams(r) }
}

// WithCommitWithin ;; commit within d and return without waiting, commits close together are merged
func WithCommitWithin(d time.Duration) UpdateOption {
	return func(r *luxirpb.UpdateRequest) {
		var ms uint64
		if d > 0 {
			ms = uint64(d.Milliseconds())
		}
		commitParams(r).CommitWithinMs = ms
	}
}

// WithWaitForMerges ;; commit, after any running merges finish
func WithWaitForMerges() UpdateOption {
	return func(r *luxirpb.UpdateRequest) { commitParams(r).WaitForMerges = true }
}

// WithMaxSegments ;; commit and merge down to at most n segments, maintenance not regular ingest
func WithMaxSegments(n uint32) UpdateOption {
	return func(r *luxirpb.UpdateRequest) { commitParams(r).MaxSegments = n }
}

// WithBuildAuxIndexes ;; commit and build missing vector overlays, "*" for all or names like "vec.title_v"
func WithBuildAuxIndexes(names ...string) UpdateOption {
	return func(r *luxirpb.UpdateRequest) {
		c := commitParams(r)
		c.BuildAuxIndexes = append(c.BuildAuxIndexes, names...)
	}
}

// WithAllOrNone ;; one failing document rolls back the whole request
func WithAllOrNone() UpdateOption {
	return func(r *luxirpb.UpdateRequest) { r.AllOrNone = true }
}

// WithAllowDups ;; skip replacing by id, faster but only safe when every id is new
func WithAllowDups() UpdateOption {
	return func(r *luxirpb.UpdateRequest) { r.AllowDups = true }
}

// WithReturnIDs ;; the response lists the ids that were indexed, in order
func WithReturnIDs() UpdateOption {
	return func(r *luxirpb.UpdateRequest) { r.ReturnIds = true }
}

// WithFieldMap ;; rename document keys onto schema fields for this call, a "" target drops the key
func WithFieldMap(m map[string]string) UpdateOption {
	return func(r *luxirpb.UpdateRequest) {
		if r.FieldMap == nil {
			r.FieldMap = make(map[string]string, len(m))
		}
		for k, v := range m {
			r.FieldMap[k] = v
		}
	}
}

// WithDropUnmapped ;; drop every key WithFieldMap doesn't name, "id" too unless it is mapped
func WithDropUnmapped() UpdateOption {
	return func(r *luxirpb.UpdateRequest) { r.DropUnmapped = true }
}

// Index ;; add documents to a collection, a document with an id that exists replaces it whole
//
// ;; collection "" defaults to collection "main", a collection that doesn't exist is made
// ;; one call is one gRPC message (Luxir takes up to 4 MiB), use IndexStream for more
// ;; returns *IndexError if some or all the documents fail
func (l *Luxir) Index(ctx context.Context, collection string, docs []Document, opts ...UpdateOption) (*luxirpb.UpdateResponse, error) {
	// make protobuf req
	req := &luxirpb.UpdateRequest{Collection: collection}

	// convert go document (map[string]any) to protobuf document
	for i, d := range docs {
		m, err := d.toMap()
		if err != nil {
			return nil, &IndexError{Msg: fmt.Sprintf("err on document: %d: %v", i, err), Err: err}
		}
		req.Docs = append(req.Docs, m)
	}

	for _, o := range opts {
		if o != nil {
			o(req)
		}
	}

	// caller, req in->req out, ctx gives deadline ;; no reply if it fails
	resp, err := l.indexer.Update(ctx, req)
	if err != nil {
		return nil, &IndexError{Err: err}
	}

	// call worked, but it can still be an err
	if resp.GetStatus() != luxirpb.UpdateResponse_OK {
		return resp, &IndexError{Resp: resp}
	}

	return resp, nil
}

// Commit ;; make everything indexed so far searchable, takes the commit options
//
// ;; returns *IndexError if the commit failed
func (l *Luxir) Commit(ctx context.Context, collection string, opts ...UpdateOption) (*luxirpb.UpdateResponse, error) {
	return l.Index(ctx, collection, nil, append([]UpdateOption{WithCommit()}, opts...)...)
}
