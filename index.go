package luxir

import (
	"context"
	"fmt"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// IndexOption ;; tweak option to an Index call
type IndexOption func(*luxirpb.UpdateRequest)

// WithCommit ;; make the documents searchable before the Index returns
func WithCommit() IndexOption {
	return func(r *luxirpb.UpdateRequest) {
		r.Commit = &luxirpb.CommitParams{}
	}
}

// Index ;; add documents to a collection
//
// ;; collection "" defaults to collection "main"
// ;; returns *IndexError if some or all the documents fail
func (l *Luxir) Index(ctx context.Context, collection string, docs []Document, opts ...IndexOption) (*luxirpb.UpdateResponse, error) {
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
		o(req)
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
