package luxir

import (
	"errors"
	"fmt"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// OpResults ;; op results by name, each is a *Facet, *SearchResult, int64, float64 or nil (rarely another value)
type OpResults map[string]any

// Facet ;; the facet called name, nil if there is none
func (r OpResults) Facet(name string) *Facet {
	f, _ := r[name].(*Facet)
	return f
}

// Docs ;; the documents op called name, nil if there is none
func (r OpResults) Docs(name string) *SearchResult {
	d, _ := r[name].(*SearchResult)
	return d
}

// Float ;; a metric as float64 (ints widen), false when it has no value
func (r OpResults) Float(name string) (float64, bool) {
	switch v := r[name].(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// Int ;; an integer metric, false when it has no value or isn't an integer
func (r OpResults) Int(name string) (int64, bool) {
	v, ok := r[name].(int64)
	return v, ok
}

// BucketRange ;; [From, To) of a RangeFacet bucket, int64 or float64 ends (dates are epoch millis)
type BucketRange struct{ From, To any }

// Bucket ;; one facet bucket
type Bucket struct {
	Value any       // ;; string, int64, or BucketRange (dates are epoch millis)
	Count int64     // ;; documents in it
	Ops   OpResults // ;; this bucket's SubOps
}

// Facet ;; buckets in the order Luxir sent them
type Facet struct {
	Buckets []Bucket
	Missing *int64 // ;; documents without a value, nil unless Missing() was asked for
}

// decodeOps ;; protobuf op results to native, nil when there are none
// ;; req is the ops that were asked for, nil when unknown
func decodeOps(m map[string]*luxirpb.Val, req map[string]*luxirpb.SearchOp) (OpResults, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(OpResults, len(m))
	for name, v := range m {
		val, err := decodeVal(v, req[name])
		if err != nil {
			return nil, fmt.Errorf("op %q: %w", name, err)
		}
		out[name] = val
	}
	return out, nil
}

// decodeVal ;; one op result, op is the op that asked for it (nil when unknown)
func decodeVal(v *luxirpb.Val, op *luxirpb.SearchOp) (any, error) {
	switch x := v.GetKind().(type) {
	case nil, *luxirpb.Val_Null:
		return nil, nil
	case *luxirpb.Val_I:
		return x.I, nil
	case *luxirpb.Val_D:
		return x.D, nil
	case *luxirpb.Val_F:
		return float64(x.F), nil
	case *luxirpb.Val_Docs:
		return decodeDocs(x.Docs, op)
	case *luxirpb.Val_Facet:
		return decodeFacet(x.Facet, op)
	default:
		return fromVal(v)
	}
}

// decodeDocs ;; a merged doc list to a *SearchResult, rows only
func decodeDocs(dl *luxirpb.DocList, op *luxirpb.SearchOp) (*SearchResult, error) {
	if len(dl.GetColumns()) > 0 {
		return nil, errors.New("documents came back as columns, expected rows")
	}
	res := &SearchResult{Found: dl.GetFound(), Offset: dl.GetOffset(), uncounted: dl.Found == nil, limit: pageLimit(op)}
	for i, m := range dl.GetDocs() {
		h, err := toHit(m)
		if err != nil {
			return nil, fmt.Errorf("hit %d: %w", i, err)
		}
		res.Hits = append(res.Hits, h)
	}
	ops, err := decodeOps(dl.GetOps(), subOps(op))
	if err != nil {
		return nil, err
	}
	res.Ops = ops
	return res, nil
}

// pageLimit ;; how many hits a full page of op holds (Luxir's default is 10), 0 when op is unknown
func pageLimit(op *luxirpb.SearchOp) int64 {
	var limit *int64
	switch k := op.GetKind().(type) {
	case *luxirpb.SearchOp_TopDocs:
		limit = k.TopDocs.Limit
	case *luxirpb.SearchOp_Fusion:
		limit = k.Fusion.Limit
	default:
		return 0
	}
	if limit == nil {
		return 10
	}
	return *limit
}

// subOps ;; the ops op asked to run under it
func subOps(op *luxirpb.SearchOp) map[string]*luxirpb.SearchOp {
	switch k := op.GetKind().(type) {
	case *luxirpb.SearchOp_TopDocs:
		return k.TopDocs.GetOps()
	case *luxirpb.SearchOp_Fusion:
		return k.Fusion.GetOps()
	case *luxirpb.SearchOp_FieldFacet:
		return k.FieldFacet.GetOps()
	case *luxirpb.SearchOp_RangeFacet:
		return k.RangeFacet.GetOps()
	case *luxirpb.SearchOp_QueryFacet:
		return k.QueryFacet.GetOps()
	}
	return nil
}

func decodeFacet(f *luxirpb.FacetResult, op *luxirpb.SearchOp) (*Facet, error) {
	keys, err := columnValues(f.GetBucketIds())
	if err != nil {
		return nil, err
	}
	counts := f.GetCounts()
	if len(keys) != len(counts) {
		return nil, fmt.Errorf("facet has %d buckets but %d counts", len(keys), len(counts))
	}

	out := &Facet{Buckets: make([]Bucket, len(keys))}
	for i := range keys {
		out.Buckets[i] = Bucket{Value: keys[i], Count: counts[i]}
	}
	if f.Missing != nil {
		m := f.GetMissing()
		out.Missing = &m
	}

	// each sub op comes as one list with a result per bucket
	subs := subOps(op)
	for name, v := range f.GetOps() {
		per := v.GetArr().GetV()
		if len(per) != len(out.Buckets) {
			return nil, fmt.Errorf("sub op %q has %d results for %d buckets", name, len(per), len(out.Buckets))
		}
		for i, e := range per {
			val, err := decodeVal(e, subs[name])
			if err != nil {
				return nil, fmt.Errorf("sub op %q bucket %d: %w", name, i, err)
			}
			if out.Buckets[i].Ops == nil {
				out.Buckets[i].Ops = OpResults{}
			}
			out.Buckets[i].Ops[name] = val
		}
	}
	return out, nil
}

// columnValues ;; a bucket id column to one value per bucket
func columnValues(c *luxirpb.Column) ([]any, error) {
	var out []any
	switch k := c.GetKind().(type) {
	case nil:
	case *luxirpb.Column_ColS:
		for _, v := range k.ColS.GetV() {
			out = append(out, v)
		}
	case *luxirpb.Column_ColI:
		for _, v := range k.ColI.GetV() {
			out = append(out, v)
		}
	case *luxirpb.Column_ColF:
		for _, v := range k.ColF.GetV() {
			out = append(out, float64(v))
		}
	case *luxirpb.Column_ColD:
		for _, v := range k.ColD.GetV() {
			out = append(out, v)
		}
	case *luxirpb.Column_MultiI:
		for _, v := range k.MultiI.GetV() {
			out = append(out, bucketRange(v.GetV()))
		}
	case *luxirpb.Column_MultiF:
		for _, v := range k.MultiF.GetV() {
			wide := make([]float64, len(v.GetV()))
			for i, f := range v.GetV() {
				wide[i] = float64(f)
			}
			out = append(out, bucketRange(wide))
		}
	case *luxirpb.Column_MultiD:
		for _, v := range k.MultiD.GetV() {
			out = append(out, bucketRange(v.GetV()))
		}
	default:
		return nil, fmt.Errorf("unsupported bucket column %T", k)
	}
	return out, nil
}

// bucketRange ;; a [from, to] pair is a BucketRange, anything else stays a slice
func bucketRange[T any](v []T) any {
	if len(v) == 2 {
		return BucketRange{From: v[0], To: v[1]}
	}
	return v
}
