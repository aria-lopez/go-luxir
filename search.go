package luxir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// opName ;; this is the op every Search and Fuse sends and reads
const opName = "hits"

// scope ;; which entry point an option may be used with
type scope uint8

const (
	forSearch scope = 1 << iota
	forDocs
	forSource
	forFuse
	forFusion
	forDo
	forScan
)

var scopeNames = map[scope]string{
	forSearch: "Search", forDocs: "Docs", forSource: "FuseSource",
	forFuse: "Fuse", forFusion: "Fusion", forDo: "Do", forScan: "Scan",
}

// optUse ;; an option that was set and where it is allowed
type optUse struct {
	name string
	ok   scope
}

// filterSpec ;; a WithFilter, except lists the ops that don't get it
type filterSpec struct {
	q      Query
	except []string
}

// searchParams ;; internal see helpers below for setters
type searchParams struct {
	offset      int64
	limit       *int64
	fields      []string
	sorts       []*luxirpb.SortSpec
	filters     []filterSpec
	freshness   time.Duration
	noCount     bool
	noScores    bool
	timeZone    string
	profile     bool
	maxParallel int32
	batchSize   int32
	requestID   string
	ops         map[string]Op
	rrf         *int32
	uses        []optUse
	errs        []error
}

// SearchOption ;; edit option to Search call
type SearchOption func(*searchParams)

func (p *searchParams) use(name string, ok scope) { p.uses = append(p.uses, optUse{name, ok}) }

// validate ;; first option that doesn't apply to s, plus any option that failed to build
func (p *searchParams) validate(s scope) error {
	for _, u := range p.uses {
		if u.ok&s == 0 {
			return fmt.Errorf("%s does not apply to %s", u.name, scopeNames[s])
		}
	}
	return errors.Join(p.errs...)
}

func newParams(opts []SearchOption) *searchParams {
	var p searchParams
	for _, o := range opts {
		if o != nil {
			o(&p)
		}
	}
	return &p
}

const (
	docScopes     = forSearch | forDocs | forFuse | forFusion
	requestScopes = forSearch | forFuse | forDo | forScan
)

// WithLimit ;; max return, default is 10 (Scan's is all), if u type in -1 u get all of em.
func WithLimit(n int64) SearchOption {
	return func(p *searchParams) {
		p.use("WithLimit", docScopes|forSource|forScan)
		if n < -1 {
			p.errs = append(p.errs, fmt.Errorf("WithLimit must be -1 or more: %d", n))
			return
		}
		p.limit = &n
	}
}

// WithOffset ;; skip this many first (for paging) ;;
func WithOffset(n int64) SearchOption {
	return func(p *searchParams) {
		p.use("WithOffset", docScopes|forScan)
		if n < 0 {
			p.errs = append(p.errs, fmt.Errorf("WithOffset must not be negative: %d", n))
			return
		}
		p.offset = n
	}
}

// WithFields ;; only return these fields of each document, "*" patterns work, default is all of em (but vectors)
func WithFields(fields ...string) SearchOption {
	return func(p *searchParams) { p.use("WithFields", docScopes|forScan); p.fields = fields }
}

func sortSpec(expr string, desc bool, vars map[string]*luxirpb.Val) *luxirpb.SortSpec {
	dir := luxirpb.SortSpec_ASC
	if desc {
		dir = luxirpb.SortSpec_DESC
	}
	return &luxirpb.SortSpec{Expr: expr, Dir: dir, Vars: vars}
}

// WithSort ;; order by field or expression (and asc or desc)
func WithSort(expr string, desc bool) SearchOption {
	return func(p *searchParams) {
		p.use("WithSort", forSearch|forDocs|forSource|forScan)
		p.sorts = append(p.sorts, sortSpec(expr, desc, nil))
	}
}

// WithSortVars ;; WithSort where the expression reads $vars
func WithSortVars(expr string, desc bool, vars map[string]any) SearchOption {
	return func(p *searchParams) {
		p.use("WithSortVars", forSearch|forDocs|forSource|forScan)
		vs, err := toVals(vars)
		if err != nil {
			p.errs = append(p.errs, &QueryError{Query: "sort", Err: err})
			return
		}
		p.sorts = append(p.sorts, sortSpec(expr, desc, vs))
	}
}

// WithFilter ;; only keep matches that also satisify another Query, does not affect ranking
// this appends to the underlying list of searchParams.filters
func WithFilter(q Query) SearchOption {
	return func(p *searchParams) {
		p.use("WithFilter", docScopes|forSource|forScan)
		p.filters = append(p.filters, filterSpec{q: q})
	}
}

// WithFilterExcept ;; WithFilter that skips the named ops, for multi-select facets
func WithFilterExcept(q Query, ops ...string) SearchOption {
	return func(p *searchParams) {
		p.use("WithFilterExcept", forSearch|forDocs)
		p.filters = append(p.filters, filterSpec{q: q, except: ops})
	}
}

// WithFreshness ;; how stale the index view may be, default 0 sees the latest commit
func WithFreshness(d time.Duration) SearchOption {
	return func(p *searchParams) {
		p.use("WithFreshness", requestScopes)
		if d < 0 {
			p.errs = append(p.errs, fmt.Errorf("WithFreshness must not be negative: %v", d))
			return
		}
		p.freshness = d
	}
}

// WithCount ;; default true, false skips counting every match so Found stays 0 and HasMore asks if the page is full
func WithCount(on bool) SearchOption {
	return func(p *searchParams) { p.use("WithCount", docScopes); p.noCount = !on }
}

// WithScores ;; default true, false leaves Hit.Score at 0
func WithScores(on bool) SearchOption {
	return func(p *searchParams) { p.use("WithScores", docScopes|forScan); p.noScores = !on }
}

// WithTimeZone ;; zone for date math and dates without an offset, ex "America/Denver", default UTC
func WithTimeZone(tz string) SearchOption {
	return func(p *searchParams) { p.use("WithTimeZone", requestScopes); p.timeZone = tz }
}

// WithProfile ;; fill SearchResult.Profile with execution details
func WithProfile() SearchOption {
	return func(p *searchParams) { p.use("WithProfile", forSearch|forFuse|forDo); p.profile = true }
}

// WithMaxParallel ;; -1 parallel, 0 engine decides, 1 serial
func WithMaxParallel(n int) SearchOption {
	return func(p *searchParams) {
		p.use("WithMaxParallel", requestScopes)
		if n < -1 || n > 1 {
			p.errs = append(p.errs, fmt.Errorf("WithMaxParallel must be -1, 0 or 1: %d", n))
			return
		}
		p.maxParallel = int32(n)
	}
}

// WithBatchSize ;; most documents per streamed batch, Luxir caps it at 256
func WithBatchSize(n int) SearchOption {
	return func(p *searchParams) { p.use("WithBatchSize", docScopes|forScan); p.batchSize = clamp32(n) }
}

// WithRequestID ;; opaque id Luxir echoes back
func WithRequestID(id string) SearchOption {
	return func(p *searchParams) { p.use("WithRequestID", requestScopes); p.requestID = id }
}

// WithOp ;; also compute op (facet, metric, ...) over the matches, read it from the result's Ops by name
func WithOp(name string, op Op) SearchOption {
	return func(p *searchParams) {
		p.use("WithOp", forSearch|forDocs|forFuse|forFusion)
		if p.ops == nil {
			p.ops = map[string]Op{}
		}
		if _, dup := p.ops[name]; dup {
			p.errs = append(p.errs, &OpError{Op: name, Msg: "defined twice"})
			return
		}
		p.ops[name] = op
	}
}

// WithRRF ;; Fuse rank constant, default 60 (0 means default too)
func WithRRF(k int) SearchOption {
	return func(p *searchParams) {
		p.use("WithRRF", forFuse|forFusion)
		if k < 0 {
			p.errs = append(p.errs, fmt.Errorf("WithRRF must not be negative: %d", k))
			return
		}
		v := clamp32(k)
		p.rrf = &v
	}
}

// topDocs ;; the one place a *luxirpb.TopDocs is made (Search and Docs both use it)
func (p *searchParams) topDocs(q Query, subs map[string]*luxirpb.SearchOp) (*luxirpb.TopDocs, error) {
	top := &luxirpb.TopDocs{
		Offset:         p.offset,
		Limit:          p.limit,
		Fields:         p.fields,
		Sort:           p.sorts,
		GetNumber:      !p.noCount,
		GetScores:      !p.noScores,
		BatchSize:      p.batchSize,
		DocumentFormat: luxirpb.DocFormat_ROWS, // return fields as per-document map instead of entire columns.
		Ops:            subs,
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
		pq, err := protoOf("filter", f.q)
		if err != nil {
			return nil, &SearchError{Msg: fmt.Sprintf("err on filter: %d: %v", i, err), Err: err}
		}
		top.Filter = append(top.Filter, &luxirpb.Filter{Query: pq, ExceptOps: f.except})
	}

	return top, nil
}

// request ;; the request-level part, ops are whatever the caller built
func (p *searchParams) request(collection string, ops map[string]*luxirpb.SearchOp) *luxirpb.SearchRequest {
	return &luxirpb.SearchRequest{
		RequestId:   p.requestID,
		Collection:  collection,
		FreshnessMs: uint64(p.freshness.Milliseconds()),
		TimeZone:    p.timeZone,
		Profile:     p.profile,
		MaxParallel: p.maxParallel,
		Ops:         ops,
	}
}

// buildOps ;; convert named ops in name order so errors are the same every run
func buildOps(ops map[string]Op) (map[string]*luxirpb.SearchOp, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make(map[string]*luxirpb.SearchOp, len(ops))
	for _, name := range names {
		if name == "" || ops[name] == nil {
			return nil, &OpError{Op: name, Msg: "needs a name and an op"}
		}
		po, err := ops[name].toProto()
		if err != nil {
			var oe *OpError
			if errors.As(err, &oe) && oe.Op == "" {
				oe.Op = name
			}
			return nil, err
		}
		out[name] = po
	}
	return out, nil
}

// Hit ;; one doc
type Hit struct {
	Score float32  // ;; ranked score (ignore this if sorting by field)
	Doc   Document // ;; the doc
}

// SearchResult ;; one page of matches
type SearchResult struct {
	Found    int64                     // ;; every match summed (not just this current page)
	Offset   int64                     // ;; rank of the first hit, the WithOffset this page was asked for
	Hits     []Hit                     // ;; this page, in ranked order as a coll of Hit
	Warnings []*luxirpb.Warning        // ;; warnings that are non fatal
	Ops      OpResults                 // ;; WithOp results by name
	Profile  *luxirpb.ExecutionProfile // ;; only with WithProfile

	uncounted bool  // ;; Found was never asked for
	limit     int64 // ;; hits in a full page, only read when uncounted
}

// HasMore ;; there are more matches beyond the current page
// to continue call Search() with WithOffset(SearchResult.NextOffset())
//
// ;; without a count a full page is the only hint, so an exact last page says true
func (r *SearchResult) HasMore() bool {
	if r.uncounted {
		return r.limit > 0 && int64(len(r.Hits)) == r.limit
	}
	return len(r.Hits) > 0 && r.NextOffset() < r.Found
}

// NextOffset ;; the offset of the page after this one, pass it to WithOffset
// ;; WARNING ;; always call SearchResult.HasMore() before relying on this
func (r *SearchResult) NextOffset() int64 {
	return r.Offset + int64(len(r.Hits))
}

// Response ;; everything one Do call answered
type Response struct {
	Ops      OpResults          // ;; every op by name
	Warnings []*luxirpb.Warning // ;; warnings that are non fatal
	Profile  *luxirpb.ExecutionProfile
}

// openSearch ;; a search stream with req sent and the send side closed
func (l *Luxir) openSearch(ctx context.Context, req *luxirpb.SearchRequest) (luxirpb.Searcher_SearchClient, error) {
	stream, err := l.searcher.Search(ctx)
	if err != nil {
		return nil, &SearchError{Err: err, Msg: fmt.Sprintf("err opening stream: %v", err)}
	}
	// check for io.EOF it means the server already ended it
	if err := stream.Send(req); err != nil && !errors.Is(err, io.EOF) {
		return nil, &SearchError{Err: err}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, &SearchError{Err: err}
	}
	return stream, nil
}

// recvSearch ;; the next batch, Luxir's own error in it is returned as an error
func recvSearch(stream luxirpb.Searcher_SearchClient) (*luxirpb.SearchResponse, error) {
	resp, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF // this does not mean the stream ended ok, it must signal more=false to be done
	}
	if err != nil {
		return nil, &SearchError{Err: err}
	}
	// we made contact but there can still be an error (also it can follow earlier batches)
	// and thats why we dont send out partial results
	if e := resp.GetError(); e != nil {
		return nil, &SearchError{Luxir: e}
	}
	return resp, nil
}

// run ;; send one request and merge its streamed batches
//
// ;; doc lists merge across batches at any depth, everything else arrives in the final one
func (l *Luxir) run(ctx context.Context, req *luxirpb.SearchRequest) (*Response, error) {
	// one stream per call, also it will cancel so there is no leaking
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := l.openSearch(ctx, req)
	if err != nil {
		return nil, err
	}

	// results come back in batches potentially, so iter through the recv
	out := &Response{}
	ops := map[string]*luxirpb.Val{}
	for {
		resp, err := recvSearch(stream)
		if err != nil {
			return nil, err
		}
		// also collect any new warningss
		out.Warnings = append(out.Warnings, resp.GetWarnings()...)
		if pr := resp.GetProfile(); pr != nil {
			out.Profile = pr
		}
		mergeOps(ops, resp.GetOps())
		if !resp.GetMore() {
			break
		}
	}

	// ;; decoded against the request so every doc list knows the page size it asked for
	if out.Ops, err = decodeOps(ops, req.GetOps()); err != nil {
		return nil, &SearchError{Msg: fmt.Sprintf("err on %v", err), Err: err}
	}
	if out.Ops == nil {
		out.Ops = OpResults{}
	}
	return out, nil
}

// mergeOps ;; fold one batch's op results into into, doc lists append at any depth, everything else is replaced
func mergeOps(into, batch map[string]*luxirpb.Val) {
	for name, v := range batch {
		dl := v.GetDocs()
		if dl == nil {
			into[name] = v
			continue
		}
		m := into[name].GetDocs()
		if m == nil {
			m = &luxirpb.DocList{Offset: dl.GetOffset()}
			into[name] = &luxirpb.Val{Kind: &luxirpb.Val_Docs{Docs: m}}
		}
		if dl.Found != nil {
			m.Found = dl.Found
		}
		if len(dl.GetColumns()) > 0 {
			m.Columns = dl.GetColumns()
		}
		m.Docs = append(m.Docs, rows(dl)...)
		if len(dl.GetOps()) > 0 {
			if m.Ops == nil {
				m.Ops = map[string]*luxirpb.Val{}
			}
			mergeOps(m.Ops, dl.GetOps())
		}
	}
}

// rows ;; one map per document in the batch, a row whose fields all missed the projection has none so it gets an empty one
func rows(dl *luxirpb.DocList) []*luxirpb.Map {
	docs := dl.GetDocs()
	if n := int(dl.GetRowCount()) - len(docs); n > 0 {
		docs = slices.Clip(docs)
		for range n {
			docs = append(docs, &luxirpb.Map{})
		}
	}
	return docs
}

// page ;; the result of the one op Search and Fuse send, with the request-level bits on it
func (p *searchParams) page(resp *Response) *SearchResult {
	res := resp.Ops.Docs(opName)
	if res == nil {
		res = &SearchResult{uncounted: p.noCount}
	}
	res.Offset = p.offset
	res.Warnings = resp.Warnings
	res.Profile = resp.Profile
	return res
}

// Search ;; find documents in a collection
//
// ;; collection "" defaults to "main"
// ;; q nil (no query) matches all docs
// ;; WithOp adds facets, metrics, etc. over the matches, they come back in SearchResult.Ops
// ;; returns *SearchError on fail, Search cannot return a partial res!
func (l *Luxir) Search(ctx context.Context, collection string, q Query, opts ...SearchOption) (*SearchResult, error) {
	p := newParams(opts)
	if err := p.validate(forSearch); err != nil {
		return nil, &SearchError{Msg: fmt.Sprintf("err on option: %v", err), Err: err}
	}
	subs, err := buildOps(p.ops)
	if err != nil {
		return nil, &SearchError{Msg: fmt.Sprintf("err on op: %v", err), Err: err}
	}
	top, err := p.topDocs(q, subs)
	if err != nil {
		return nil, err
	}

	req := p.request(collection, map[string]*luxirpb.SearchOp{opName: {Kind: &luxirpb.SearchOp_TopDocs{TopDocs: top}}})
	resp, err := l.run(ctx, req)
	if err != nil {
		return nil, err
	}
	return p.page(resp), nil
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
