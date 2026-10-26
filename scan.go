package luxir

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Scan ;; every match one hit at a time without holding them all, for exports and big reads
//
// ;; collection "" defaults to "main", q nil matches all docs
// ;; default limit is all (-1), hits are fetched in batches of WithBatchSize (Luxir caps it at 256)
// ;; break out of the loop to stop early
// ;; a failure is yielded once as a *SearchError and ends the loop, hits before it may belong to the failed request
func (l *Luxir) Scan(ctx context.Context, collection string, q Query, opts ...SearchOption) iter.Seq2[Hit, error] {
	return func(yield func(Hit, error) bool) {
		p := newParams(opts)
		if err := p.validate(forScan); err != nil {
			yield(Hit{}, &SearchError{Msg: fmt.Sprintf("err on option: %v", err), Err: err})
			return
		}
		if p.limit == nil {
			all := int64(-1)
			p.limit = &all
		}
		p.noCount = true
		top, err := p.topDocs(q, nil)
		if err != nil {
			yield(Hit{}, err)
			return
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stream, err := l.openSearch(ctx, p.request(collection, map[string]*luxirpb.SearchOp{opName: {Kind: &luxirpb.SearchOp_TopDocs{TopDocs: top}}}))
		if err != nil {
			yield(Hit{}, err)
			return
		}
		for {
			resp, err := recvSearch(stream)
			if err != nil {
				yield(Hit{}, err)
				return
			}
			dl := resp.GetOps()[opName].GetDocs()
			if len(dl.GetColumns()) > 0 {
				err := errors.New("documents came back as columns, expected rows")
				yield(Hit{}, &SearchError{Msg: err.Error(), Err: err})
				return
			}
			for i, m := range rows(dl) {
				h, err := toHit(m)
				if err != nil {
					yield(Hit{}, &SearchError{Msg: fmt.Sprintf("err on hit %d: %v", i, err), Err: err})
					return
				}
				if !yield(h, nil) {
					return
				}
			}
			if !resp.GetMore() {
				return
			}
		}
	}
}
