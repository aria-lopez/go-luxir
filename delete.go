package luxir

import (
	"context"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Delete ;; removes document(s) from a collect given a list of ids
//
// ;; collection "" defaults to collection "main"
// ;; we skip ids that aren't real
// ;; uses the same opts as Index w/ UpdateOption
// ;; returns *DeleteError if the call or req failed
func (l *Luxir) Delete(ctx context.Context, collection string, ids []string, opts ...UpdateOption) (*luxirpb.UpdateResponse, error) {
	if len(ids) == 0 {
		return nil, &DeleteError{Msg: "no provided ids"}
	}
	if collection == "" {
		collection = "main"
	}
	req := &luxirpb.UpdateRequest{Collection: collection, DeleteIds: ids}
	for _, o := range opts {
		if o != nil {
			o(req) // bind opts to req
		}
	}

	resp, err := l.indexer.Update(ctx, req)
	if err != nil {
		return nil, &DeleteError{Err: err}
	}

	// if a single id fails we cant catch that; so no ok mean bad request
	if resp.GetStatus() != luxirpb.UpdateResponse_OK {
		return resp, &DeleteError{Resp: resp}
	}

	return resp, nil
}
