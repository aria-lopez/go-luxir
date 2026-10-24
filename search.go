package luxir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// opName ;; this is the op every Search sends and reads
const opName = "hits"

// Query ;; convert to proto
//
// ;; basically this and queryFunc are used to allow us control pre-query
// ;; and capture any errors.
type Query interface {
	toProto() (*luxirpb.Query, error)
}
type queryFunc func() (*luxirpb.Query, error)

func (f queryFunc) toProto() (*luxirpb.Query, error) { return f() }

// Match ;; documents whose field hold the (matched) value
func Match(field string, v any) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		val, err := toVal(v)
		if err != nil {
			return nil, &MatchError{Err: err, Msg: fmt.Sprintf("[go-luxir] match err, bad toVal %q: %w", field, err)}
		}
		m := &luxirpb.Match{Field: field, Val: val}
		return &luxirpb.Query{Kind: &luxirpb.Query_Match{Match: m}}, nil
	})
}

// Simple ;; forgiving search eg; words, "phrases", +must, -not, a | b, prefix*, word~ ... you get the point
//
// ;; FIXME: [easy] [api] [errors] does this need to expose the err into a Luxir.ERROR_TYPE?
func Simple(q string, fields ...string) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		s := &luxirpb.SimpleQuery{Q: q, Fields: fields}
		return &luxirpb.Query{Kind: &luxirpb.Query_SimpleQuery{SimpleQuery: s}}, nil
	})
}

// Raw ;; for any query that doesnt have a helper (returns as protobuf query)
func Raw(q *luxirpb.Query) Query {
	return queryFunc(func() (*luxirpb.Query, error) { return q, nil })
}

// searchParams ;; internal see helpers below for setters
type searchParams struct {
	offset    int64
	limit     *int64
	fields    []string
	sorts     []*luxirpb.SortSpec
	filters   []Query
	freshness time.Duration
}

// SearchOption ;; edit option to Search call
type SearchOption func(*searchParams)

// WithLimit ;; max return, default is 10, if u type in -1 u get all of em.
func WithLimit(n int64) SearchOption { return func(p *searchParams) { p.limit = &n } }

// WithOffset ;; skip this many first (for paging) ;;
func WithOffset(n int64) SearchOption { return func(p *searchParams) { p.offset = n } }

// WithFields ;; only return documents with fields. default is all of em
func WithFields(fields ...string) SearchOption { return func(p *searchParams) { p.fields = fields } }

// WithSort ;; order by field or expression (and asc or desc)
func WithSort(expr string, desc bool) SearchOption {
	return func(p *searchParams) {
		dir := luxirpb.SortSpec_ASC
		if desc {
			dir = luxirpb.SortSpec_DESC
		}
		p.sorts = append(p.sorts, &luxirpb.SortSpec{Expr: expr, Dir: dir})
	}
}

// WithFilter ;; only keep matches that also satisify another Query, does not affect ranking
// this appends to the underlying list of searchParams.filters ([]Query)
func WithFilter(q Query) SearchOption {
	return func(p *searchParams) { p.filters = append(p.filters, q) }
}

// WithFreshness ;; only match up to passed time.
func WithFreshness(d time.Duration) SearchOption { return func(p *searchParams) { p.freshness = d } }

// Hit ;; one doc
type Hit struct {
	Score float32  // ;; ranked score (ignore this if sorting by field)
	Doc   Document // ;; the doc
}

// SearchResult ;; one page of matches
type SearchResult struct {
	Found    int64              // ;; every match summed (not just this current page)
	Offset   int64              // ;; rank of the first hit, the WithOffset this page was asked for
	Hits     []Hit              // ;; this page, in ranked order as a coll of Hit
	Warnings []*luxirpb.Warning // ;; warnings that are non fatal
}

// HasMore ;; there are more matches beyond the current page
// to continue call Search() with WithOffset(SearchResult.NextOffset())
func (r *SearchResult) HasMore() bool {
	return len(r.Hits) > 0 && r.NextOffset() < r.Found
}

// NextOffset ;; the offset of the page after this one, pass it to WithOffset
// ;; WARNING ;; always call SearchResult.HasMore() before relying on this
func (r *SearchResult) NextOffset() int64 {
	return r.Offset + int64(len(r.Hits))
}

// Search ;; find documents in a collection
//
// ;; collection "" defaults to "main"
// ;; q nil (no query) matches all docs
// ;; returns *SearchError on fail, Search cannot return a partial res!
func (l *Luxir) Search(ctx context.Context, collection string, q Query, opts ...SearchOption) (*SearchResult, error) {
	var p searchParams
	for _, o := range opts {
		o(&p) // apply opts
	}

	top := &luxirpb.TopDocs{
		Offset:         p.offset,
		Limit:          p.limit,
		Fields:         p.fields,
		Sort:           p.sorts,
		GetNumber:      true,                   // idk if this should be optional
		GetScores:      true,                   // or this
		DocumentFormat: luxirpb.DocFormat_ROWS, // return fields as per-document map instead of entire columns.
	}

	// if query is not nil format toProto and apply
	if q != nil {
		pq, err := q.toProto()
		if err != nil {
			return nil, &SearchError{Msg: fmt.Sprintf("err on query: %v", err), Err: err}
		}
		top.Query = pq
	}

	// apply filters if any are provided
	for i, f := range p.filters {
		pq, err := f.toProto()
		if err != nil {
			return nil, &SearchError{Msg: fmt.Sprintf("err on filter: %d: %v", i, err), Err: err}
		}
		top.Filter = append(top.Filter, &luxirpb.Filter{Query: pq})
	}

	// build actual req
	req := &luxirpb.SearchRequest{
		Collection:  collection,
		FreshnessMs: uint64(p.freshness.Milliseconds()),
		Ops:         map[string]*luxirpb.SearchOp{opName: {Kind: &luxirpb.SearchOp_TopDocs{TopDocs: top}}},
	}

	// one stream per call, also it will cancel so there is no leaking
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := l.searcher.Search(ctx)
	if err != nil {
		return nil, &SearchError{Err: err, Msg: fmt.Sprintf("err building search: %v", err)}
	}
	// check for io.EOF it means the server already ended it
	if err := stream.Send(req); err != nil && !errors.Is(err, io.EOF) {
		return nil, &SearchError{Err: err}
	}
	// FIXME: this can error
	stream.CloseSend()

	// results come back in batches potentially, so iter through the recv
	res := &SearchResult{Offset: p.offset}
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF // this does not mean the stream ended ok, it must signal more=false to be done
		}
		if err != nil {
			return nil, &SearchError{Err: err}
		}
		// we made contact but there can still be an error (also it can follow earlier batches)
		// and thats why we dont send out partial results
		if resp.GetError() != nil {
			return nil, &SearchError{Err: err}
		}
		// also collect any new warningss
		res.Warnings = append(res.Warnings, resp.GetWarnings()...)

		// dl=Document List (basically our results)
		if dl := resp.GetOps()[opName].GetDocs(); dl != nil {
			if dl.Found != nil {
				res.Found = dl.GetFound()
			}
			for i, m := range dl.GetDocs() {
				h, err := toHit(m)
				if err != nil {
					return nil, &SearchError{Msg: fmt.Sprintf("err on hit: %d: %v", len(res.Hits)+i, err), Err: err}
				}
				res.Hits = append(res.Hits, h)
			}
		}
		if !resp.GetMore() {
			return res, nil
		}
	}
}

// toHit ;; change proto doc to a Hit
func toHit(m *luxirpb.Map) (Hit, error) {
	d, err := fromMap(m) // document.go decoder
	if err != nil {
		return Hit{}, err
	}
	var h Hit
	// convert _score_ to native type
	switch s := d["_score_"].(type) {
	case float32:
		h.Score = s
	case float64:
		h.Score = float32(s)
	}
	delete(d, "_score_")
	h.Doc = d

	return h, nil
}
