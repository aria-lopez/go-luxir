package luxir

import (
	"context"
	"fmt"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Fuse ;; merge ranked sources into one list with reciprocal rank fusion (hybrid search)
//
// ;; collection "" defaults to "main"
// ;; each source takes WithLimit / WithFilter / WithSort, they are the candidate pools
// ;; the options here shape the merged list, WithSort doesn't apply
// ;; returns *SearchError on fail, same as Search
func (l *Luxir) Fuse(ctx context.Context, collection string, sources []Source, opts ...SearchOption) (*SearchResult, error) {
	p := newParams(opts)
	if err := p.validate(forFuse); err != nil {
		return nil, &SearchError{Msg: fmt.Sprintf("err on option: %v", err), Err: err}
	}
	f, err := p.fusionProto(sources)
	if err != nil {
		return nil, wrapOpErr(err)
	}

	req := p.request(collection, map[string]*luxirpb.SearchOp{opName: {Kind: &luxirpb.SearchOp_Fusion{Fusion: f}}})
	resp, err := l.run(ctx, req)
	if err != nil {
		return nil, err
	}
	return p.page(resp), nil
}

// Do ;; send several named ops in one request, each over the whole collection unless it has a domain
//
// ;; collection "" defaults to "main"
// ;; options are request level only: WithFreshness / WithTimeZone / WithProfile / WithMaxParallel / WithRequestID
// ;; returns *SearchError on fail, Do cannot return a partial res!
func (l *Luxir) Do(ctx context.Context, collection string, ops map[string]Op, opts ...SearchOption) (*Response, error) {
	p := newParams(opts)
	if err := p.validate(forDo); err != nil {
		return nil, &SearchError{Msg: fmt.Sprintf("err on option: %v", err), Err: err}
	}
	if len(ops) == 0 {
		err := &OpError{Msg: "Do needs at least one op"}
		return nil, &SearchError{Msg: fmt.Sprintf("err on op: %v", err), Err: err}
	}
	built, err := buildOps(ops)
	if err != nil {
		return nil, wrapOpErr(err)
	}

	return l.run(ctx, p.request(collection, built))
}

// wrapOpErr ;; an op build error as a *SearchError, one that already is one passes through
func wrapOpErr(err error) error {
	if _, ok := err.(*SearchError); ok {
		return err
	}
	return &SearchError{Msg: fmt.Sprintf("err on op: %v", err), Err: err}
}
