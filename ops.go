package luxir

import (
	"errors"
	"fmt"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Op ;; a search operation, a facet / metric / documents / fusion
type Op interface {
	toProto() (*luxirpb.SearchOp, error)
}

type opKind uint8

const (
	kField opKind = 1 << iota
	kRange
	kQuery
	kExpr
	kDocs
	kFusion
	kDomain
	kFacets = kField | kRange | kQuery
	kAny    = kFacets | kExpr | kDocs | kFusion | kDomain
)

// NamedQuery ;; a QueryFacet bucket
type NamedQuery struct {
	Name  string
	Query Query
}

// Named ;; label a query for QueryFacet
func Named(name string, q Query) NamedQuery { return NamedQuery{name, q} }

// opSpec ;; every Op is one of these, OpOptions fill what their kind takes
type opSpec struct {
	kind opKind
	uses []optKind
	errs []error

	field    string
	limit    *int64
	minCount *int64
	missing  bool
	sorts    []*luxirpb.SortSpec
	subs     map[string]Op
	selected []any
	selMode  luxirpb.SelectionMode
	selSet   bool

	start, end, gap any
	cal             *luxirpb.CalendarGap
	tz              string

	buckets []NamedQuery

	expr string
	vars map[string]any

	docsQ    Query
	sources  []Source
	docsOpts []SearchOption

	domQ        Query
	domFilter   []Query
	applyParent bool
	hasDomain   bool
}

// optKind ;; an OpOption that was used and which kinds take it
type optKind struct {
	name string
	ok   opKind
}

// OpOption ;; edit an Op, one the kind doesn't take is an *OpError
type OpOption func(*opSpec)

func (s *opSpec) use(name string, ok opKind) { s.uses = append(s.uses, optKind{name, ok}) }

func newOp(s *opSpec, opts []OpOption) Op {
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

// FieldFacet ;; count documents per distinct value of the field
func FieldFacet(field string, opts ...OpOption) Op {
	return newOp(&opSpec{kind: kField, field: field}, opts)
}

// RangeFacet ;; count documents per [start, end) bucket, needs Gap or CalendarGap
func RangeFacet(field string, start, end any, opts ...OpOption) Op {
	return newOp(&opSpec{kind: kRange, field: field, start: start, end: end}, opts)
}

// QueryFacet ;; count documents per named query, a document can be in several
func QueryFacet(buckets []NamedQuery, opts ...OpOption) Op {
	return newOp(&opSpec{kind: kQuery, buckets: buckets}, opts)
}

// Metric ;; any value expression over the matches, ex Metric("avg(price_d)", nil)
func Metric(expr string, vars map[string]any) Op {
	return &opSpec{kind: kExpr, expr: expr, vars: vars}
}

// Avg ;; average of a value expression
func Avg(expr string) Op { return Metric("avg("+expr+")", nil) }

// Sum ;; sum of a value expression
func Sum(expr string) Op { return Metric("sum("+expr+")", nil) }

// Min ;; smallest value of a value expression
func Min(expr string) Op { return Metric("min("+expr+")", nil) }

// Max ;; largest value of a value expression
func Max(expr string) Op { return Metric("max("+expr+")", nil) }

// Count ;; total values a numeric field has across the domain, for a document count use Found
func Count(expr string) Op { return Metric("sum(count("+expr+"))", nil) }

// Docs ;; ranked documents as an op, ex the best few in every facet bucket
func Docs(q Query, opts ...SearchOption) Op {
	return &opSpec{kind: kDocs, docsQ: q, docsOpts: opts}
}

// Source ;; one ranked list for Fuse
type Source struct {
	name string
	q    Query
	opts []SearchOption
}

// FuseSource ;; a named ranked list, takes WithLimit / WithFilter / WithSort
func FuseSource(name string, q Query, opts ...SearchOption) Source {
	return Source{name, q, opts}
}

// Fusion ;; merge ranked sources into one list (reciprocal rank fusion), for Do
func Fusion(sources []Source, opts ...SearchOption) Op {
	return &opSpec{kind: kFusion, sources: sources, docsOpts: opts}
}

// SubOp ;; run another op inside every bucket
func SubOp(name string, op Op) OpOption {
	return func(s *opSpec) {
		s.use("SubOp", kFacets)
		if s.subs == nil {
			s.subs = map[string]Op{}
		}
		if _, dup := s.subs[name]; dup {
			s.errs = append(s.errs, &OpError{Op: name, Msg: "defined twice"})
			return
		}
		s.subs[name] = op
	}
}

// FacetLimit ;; most buckets, default 5, -1 for all
func FacetLimit(n int64) OpOption {
	return func(s *opSpec) { s.use("FacetLimit", kField); s.limit = &n }
}

// MinCount ;; fewest documents a bucket may have
func MinCount(n int64) OpOption {
	return func(s *opSpec) { s.use("MinCount", kField|kRange); s.minCount = &n }
}

// Missing ;; also count the documents without a value
func Missing() OpOption {
	return func(s *opSpec) { s.use("Missing", kField|kRange); s.missing = true }
}

// SortByMetric ;; order buckets by one of their SubOp metrics
func SortByMetric(op string, desc bool) OpOption {
	return func(s *opSpec) {
		s.use("SortByMetric", kField)
		s.sorts = []*luxirpb.SortSpec{sortSpec(op, desc, nil)}
	}
}

// Select ;; narrow the results to documents in any of these buckets, a bucket's Value can be sent back
func Select(values ...any) OpOption {
	return func(s *opSpec) {
		s.use("Select", kFacets)
		s.selSet, s.selected, s.selMode = true, values, luxirpb.SelectionMode_ANY
	}
}

// SelectAll ;; narrow the results to documents in every one of these buckets
func SelectAll(values ...any) OpOption {
	return func(s *opSpec) {
		s.use("SelectAll", kFacets)
		s.selSet, s.selected, s.selMode = true, values, luxirpb.SelectionMode_ALL
	}
}

// Gap ;; fixed bucket width, milliseconds for dates
func Gap(v any) OpOption { return func(s *opSpec) { s.use("Gap", kRange); s.gap = v } }

// CalendarGap ;; civil buckets for dates, ex CalendarGap(1, luxirpb.CalendarGap_MONTH)
func CalendarGap(n int, unit luxirpb.CalendarGap_Unit) OpOption {
	return func(s *opSpec) {
		s.use("CalendarGap", kRange)
		s.cal = &luxirpb.CalendarGap{N: int32(n), Unit: unit}
	}
}

// FacetTimeZone ;; zone for this facet's calendar buckets, default is the search's
func FacetTimeZone(tz string) OpOption {
	return func(s *opSpec) { s.use("FacetTimeZone", kRange); s.tz = tz }
}

// WithDomainQuery ;; run over this query's matches instead of the search's
func WithDomainQuery(q Query) OpOption {
	return func(s *opSpec) { s.use("WithDomainQuery", kAny); s.hasDomain, s.domQ = true, q }
}

// WithDomainFilter ;; narrow this op's documents further
func WithDomainFilter(qs ...Query) OpOption {
	return func(s *opSpec) {
		s.use("WithDomainFilter", kAny)
		s.hasDomain = true
		s.domFilter = append(s.domFilter, qs...)
	}
}

// ApplyParentFilters ;; with WithDomainQuery, keep the search's filters and selections
func ApplyParentFilters() OpOption {
	return func(s *opSpec) { s.use("ApplyParentFilters", kAny); s.hasDomain, s.applyParent = true, true }
}

// domainOp ;; an op with a domain put on it from outside
type domainOp struct {
	op   Op
	spec opSpec
}

// InDomain ;; run any op over a different set of documents, takes WithDomainQuery / WithDomainFilter / ApplyParentFilters
func InDomain(op Op, opts ...OpOption) Op {
	d := &domainOp{op: op, spec: opSpec{kind: kDomain}}
	for _, o := range opts {
		if o != nil {
			o(&d.spec)
		}
	}
	return d
}

func (d *domainOp) toProto() (*luxirpb.SearchOp, error) {
	for _, u := range d.spec.uses {
		if u.ok&kDomain == 0 {
			return nil, &OpError{Msg: u.name + " does not apply to InDomain"}
		}
	}
	if d.op == nil {
		return nil, &OpError{Msg: "InDomain needs an op"}
	}
	po, err := d.op.toProto()
	if err != nil {
		return nil, err
	}
	if po.Domain != nil {
		return nil, &OpError{Msg: "the op already has a domain"}
	}
	if po.Domain, err = d.spec.domain(); err != nil {
		return nil, err
	}
	return po, nil
}

func (s *opSpec) toProto() (*luxirpb.SearchOp, error) {
	for _, u := range s.uses {
		if u.ok&s.kind == 0 {
			return nil, &OpError{Msg: u.name + " does not apply to this kind of op"}
		}
	}
	if err := errors.Join(s.errs...); err != nil {
		return nil, err
	}

	subs, err := buildOps(s.subs)
	if err != nil {
		return nil, err
	}

	op := &luxirpb.SearchOp{}
	switch s.kind {
	case kField:
		f := &luxirpb.FieldFacet{Field: s.field, Limit: s.limit, Mincount: s.minCount, Missing: s.missing, Sort: s.sorts, Ops: subs}
		if f.Selected, f.SelectionMode, err = s.selection(); err != nil {
			return nil, err
		}
		op.Kind = &luxirpb.SearchOp_FieldFacet{FieldFacet: f}
	case kRange:
		r, err := s.rangeFacet(subs)
		if err != nil {
			return nil, err
		}
		op.Kind = &luxirpb.SearchOp_RangeFacet{RangeFacet: r}
	case kQuery:
		q, err := s.queryFacet(subs)
		if err != nil {
			return nil, err
		}
		op.Kind = &luxirpb.SearchOp_QueryFacet{QueryFacet: q}
	case kExpr:
		vs, err := toVals(s.vars)
		if err != nil {
			return nil, &OpError{Err: err}
		}
		op.Kind = &luxirpb.SearchOp_ExprOp{ExprOp: &luxirpb.ExprOp{Expr: s.expr, Vars: vs}}
	case kDocs:
		p := newParams(s.docsOpts)
		if err := p.validate(forDocs); err != nil {
			return nil, &OpError{Err: err}
		}
		top, err := p.docsProto(s.docsQ)
		if err != nil {
			return nil, err
		}
		op.Kind = &luxirpb.SearchOp_TopDocs{TopDocs: top}
	case kFusion:
		p := newParams(s.docsOpts)
		if err := p.validate(forFusion); err != nil {
			return nil, &OpError{Err: err}
		}
		f, err := p.fusionProto(s.sources)
		if err != nil {
			return nil, err
		}
		op.Kind = &luxirpb.SearchOp_Fusion{Fusion: f}
	}

	if s.hasDomain {
		if op.Domain, err = s.domain(); err != nil {
			return nil, err
		}
	}
	return op, nil
}

// selection ;; the Select / SelectAll values, nil Val when none were set
func (s *opSpec) selection() (*luxirpb.Val, luxirpb.SelectionMode, error) {
	if !s.selSet {
		return nil, luxirpb.SelectionMode_ANY, nil
	}
	v, err := toVal(s.selected)
	if err != nil {
		return nil, 0, &OpError{Msg: "selected: " + err.Error(), Err: err}
	}
	return v, s.selMode, nil
}

func (s *opSpec) rangeFacet(subs map[string]*luxirpb.SearchOp) (*luxirpb.RangeFacet, error) {
	if s.start == nil || s.end == nil {
		return nil, &OpError{Msg: "needs a start and an end"}
	}
	start, err1 := toVal(s.start)
	end, err2 := toVal(s.end)
	if err := errors.Join(err1, err2); err != nil {
		return nil, &OpError{Msg: "start/end: " + err.Error(), Err: err}
	}
	if (s.gap == nil) == (s.cal == nil) {
		return nil, &OpError{Msg: "needs exactly one of Gap or CalendarGap"}
	}

	r := &luxirpb.RangeFacet{Field: s.field, Start: start, End: end, Mincount: s.minCount, Missing: s.missing, Ops: subs, TimeZone: s.tz}
	if s.cal != nil {
		r.GapKind = &luxirpb.RangeFacet_CalendarGap{CalendarGap: s.cal}
	} else {
		g, err := toVal(s.gap)
		if err != nil {
			return nil, &OpError{Msg: "gap: " + err.Error(), Err: err}
		}
		r.GapKind = &luxirpb.RangeFacet_Gap{Gap: g}
	}
	r.Selected, r.SelectionMode, err1 = s.selection()
	if err1 != nil {
		return nil, err1
	}
	return r, nil
}

func (s *opSpec) queryFacet(subs map[string]*luxirpb.SearchOp) (*luxirpb.QueryFacet, error) {
	if len(s.buckets) == 0 {
		return nil, &OpError{Msg: "needs at least one bucket"}
	}
	q := &luxirpb.QueryFacet{Ops: subs}
	seen := map[string]bool{}
	for _, b := range s.buckets {
		if b.Name == "" || seen[b.Name] {
			return nil, &OpError{Msg: fmt.Sprintf("bucket %q needs a unique name", b.Name)}
		}
		seen[b.Name] = true
		pq, err := protoOf("query facet", b.Query)
		if err != nil {
			return nil, err
		}
		q.Buckets = append(q.Buckets, &luxirpb.QueryBucket{Name: b.Name, Query: pq})
	}
	var err error
	if q.Selected, q.SelectionMode, err = s.selection(); err != nil {
		return nil, err
	}
	return q, nil
}

func (s *opSpec) domain() (*luxirpb.Domain, error) {
	d := &luxirpb.Domain{}
	if s.domQ != nil {
		pq, err := s.domQ.toProto()
		if err != nil {
			return nil, err
		}
		d.Query = pq
	}
	if s.applyParent {
		if s.domQ == nil {
			return nil, &OpError{Msg: "ApplyParentFilters needs WithDomainQuery"}
		}
		d.ApplyParentFilters = &s.applyParent
	}
	for _, f := range s.domFilter {
		pq, err := protoOf("domain filter", f)
		if err != nil {
			return nil, err
		}
		d.Filter = append(d.Filter, pq)
	}
	return d, nil
}

// docsProto ;; TopDocs for a Docs op, its WithOp ops are built here
func (p *searchParams) docsProto(q Query) (*luxirpb.TopDocs, error) {
	subs, err := buildOps(p.ops)
	if err != nil {
		return nil, err
	}
	return p.topDocs(q, subs)
}

// fusionProto ;; Fusion for Fuse and the Fusion op, p holds the fusion level options
func (p *searchParams) fusionProto(sources []Source) (*luxirpb.Fusion, error) {
	if len(sources) == 0 {
		return nil, &OpError{Msg: "fusion needs at least one source"}
	}
	subs, err := buildOps(p.ops)
	if err != nil {
		return nil, err
	}

	f := &luxirpb.Fusion{
		Sources:        map[string]*luxirpb.TopDocs{},
		Limit:          p.limit,
		Offset:         p.offset,
		GetNumber:      !p.noCount,
		GetScores:      !p.noScores,
		Fields:         p.fields,
		BatchSize:      p.batchSize,
		DocumentFormat: luxirpb.DocFormat_ROWS,
		Ops:            subs,
		Method:         &luxirpb.Fusion_Rrf{Rrf: &luxirpb.RrfFusion{}},
	}
	if p.rrf != nil {
		f.Method = &luxirpb.Fusion_Rrf{Rrf: &luxirpb.RrfFusion{K: *p.rrf}}
	}
	for i, fl := range p.filters {
		pq, err := protoOf("filter", fl.q)
		if err != nil {
			return nil, &SearchError{Msg: fmt.Sprintf("err on filter: %d: %v", i, err), Err: err}
		}
		f.Filter = append(f.Filter, &luxirpb.Filter{Query: pq})
	}

	for _, src := range sources {
		if src.name == "" || f.Sources[src.name] != nil {
			return nil, &OpError{Msg: fmt.Sprintf("source %q needs a unique name", src.name)}
		}
		sp := newParams(src.opts)
		if err := sp.validate(forSource); err != nil {
			return nil, &OpError{Op: src.name, Err: err}
		}
		top := &luxirpb.TopDocs{Limit: sp.limit, Sort: sp.sorts}
		if src.q != nil {
			if top.Query, err = src.q.toProto(); err != nil {
				return nil, &SearchError{Msg: fmt.Sprintf("err on source %q: %v", src.name, err), Err: err}
			}
		}
		for _, fl := range sp.filters {
			pq, err := protoOf("filter", fl.q)
			if err != nil {
				return nil, &SearchError{Msg: fmt.Sprintf("err on source %q: %v", src.name, err), Err: err}
			}
			top.Filter = append(top.Filter, &luxirpb.Filter{Query: pq})
		}
		f.Sources[src.name] = top
	}
	return f, nil
}
