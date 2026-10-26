package luxir

import (
	"fmt"
	"math"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Query ;; convert to proto
//
// ;; basically this and queryFunc are used to allow us control pre-query
// ;; and capture any errors.
type Query interface {
	toProto() (*luxirpb.Query, error)
}
type queryFunc func() (*luxirpb.Query, error)

func (f queryFunc) toProto() (*luxirpb.Query, error) { return f() }

// clamp32 ;; n as an int32, out of range saturates instead of wrapping so a huge value stays huge and range checks still catch it
func clamp32(n int) int32 { return int32(min(max(n, math.MinInt32), math.MaxInt32)) }

// Raw ;; for any query that doesnt have a helper (returns as protobuf query)
func Raw(q *luxirpb.Query) Query {
	return queryFunc(func() (*luxirpb.Query, error) { return q, nil })
}

// toVals ;; native vars to protobuf vars
func toVals(m map[string]any) (map[string]*luxirpb.Val, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]*luxirpb.Val, len(m))
	for k, v := range m {
		val, err := toVal(v)
		if err != nil {
			return nil, fmt.Errorf("var %q: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

// protoOf ;; convert q, a nil q is an error naming the helper
func protoOf(query string, q Query) (*luxirpb.Query, error) {
	if q == nil {
		return nil, queryErr(query, "query is nil")
	}
	return q.toProto()
}

// matchSpec ;; what MatchOption edits
type matchSpec struct {
	op       luxirpb.Match_Operator
	minMatch int
	allowed  []string
}

// MatchOption ;; edit a Match or SimpleWith
type MatchOption func(*matchSpec)

// RequireAll ;; every term has to match, default is any
func RequireAll() MatchOption { return func(m *matchSpec) { m.op = luxirpb.Match_AND } }

// MinMatch ;; at least n terms have to match, overrides RequireAll
func MinMatch(n int) MatchOption { return func(m *matchSpec) { m.minMatch = n } }

// AllowedFields ;; SimpleWith only, narrows which field:value names the text may reach
func AllowedFields(fields ...string) MatchOption {
	return func(m *matchSpec) { m.allowed = append(m.allowed, fields...) }
}

func newMatchSpec(opts []MatchOption) *matchSpec {
	var m matchSpec
	for _, o := range opts {
		if o != nil {
			o(&m)
		}
	}
	return &m
}

// Match ;; documents whose field hold the (matched) value
func Match(field string, v any, opts ...MatchOption) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		val, err := toVal(v)
		if err != nil {
			return nil, &MatchError{Err: err, Msg: fmt.Sprintf("[go-luxir] match %q: %v", field, err)}
		}
		s := newMatchSpec(opts)
		if s.allowed != nil {
			return nil, queryErr("match", "AllowedFields only applies to SimpleWith")
		}
		if s.minMatch < 0 {
			return nil, queryErr("match", "min match must not be negative: %d", s.minMatch)
		}
		m := &luxirpb.Match{Field: field, Val: val, Operator: s.op, MinMatch: clamp32(s.minMatch)}
		return &luxirpb.Query{Kind: &luxirpb.Query_Match{Match: m}}, nil
	})
}

// Simple ;; forgiving search eg; words, "phrases", +must, -not, a | b, prefix*, word~ ... you get the point
//
// ;; safe for raw user input, bad syntax degrades instead of failing, needs at least one field
func Simple(q string, fields ...string) Query { return SimpleWith(q, fields) }

// SimpleWith ;; Simple with options, RequireAll / MinMatch / AllowedFields
func SimpleWith(q string, fields []string, opts ...MatchOption) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		if len(fields) == 0 {
			return nil, queryErr("simple", "needs at least one field for bare words")
		}
		s := newMatchSpec(opts)
		if s.minMatch < 0 {
			return nil, queryErr("simple", "min match must not be negative: %d", s.minMatch)
		}
		sq := &luxirpb.SimpleQuery{Q: q, Fields: fields, Operator: s.op, MinMatch: clamp32(s.minMatch), AllowedFields: s.allowed}
		return &luxirpb.Query{Kind: &luxirpb.Query_SimpleQuery{SimpleQuery: sq}}, nil
	})
}

// All ;; every document
func All() Query {
	return Raw(&luxirpb.Query{Kind: &luxirpb.Query_All{All: true}})
}

// Exists ;; documents with any value in the field
func Exists(field string) Query {
	return Raw(&luxirpb.Query{Kind: &luxirpb.Query_Exists{Exists: &luxirpb.ExistsQuery{Field: field}}})
}

// Expr ;; Luxir query language, user input goes in vars (bound as $name, never parsed)
func Expr(q string, vars map[string]any) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		vs, err := toVals(vars)
		if err != nil {
			return nil, &QueryError{Query: "expr", Err: err}
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Expr{Expr: &luxirpb.ExprQuery{Q: q, Vars: vs}}}, nil
	})
}

// Prefix ;; a term in the field starts with prefix
func Prefix(field, prefix string) Query {
	return Raw(&luxirpb.Query{Kind: &luxirpb.Query_Prefix{Prefix: &luxirpb.PrefixQuery{Field: field, Prefix: prefix}}})
}

// Wildcard ;; * is any bytes, ? is one character, \ escapes
func Wildcard(field, pattern string) Query {
	return Raw(&luxirpb.Query{Kind: &luxirpb.Query_Wildcard{Wildcard: &luxirpb.WildcardQuery{Field: field, Pattern: pattern}}})
}

// Regex ;; matches a whole term
func Regex(field, pattern string) Query {
	return Raw(&luxirpb.Query{Kind: &luxirpb.Query_Regex{Regex: &luxirpb.RegexQuery{Field: field, Pattern: pattern}}})
}

// PhraseOption ;; edit a Phrase
type PhraseOption func(*luxirpb.PhraseQuery)

// Slop ;; how far the words may drift apart, 0 is exact
func Slop(n int) PhraseOption { return func(p *luxirpb.PhraseQuery) { p.Slop = clamp32(n) } }

// Phrase ;; the words of text next to each other, in order
func Phrase(field, text string, opts ...PhraseOption) Query {
	return phrase(field, text, nil, opts)
}

// PhraseWords ;; Phrase from words you already split
func PhraseWords(field string, words []string, opts ...PhraseOption) Query {
	return phrase(field, "", words, opts)
}

func phrase(field, text string, words []string, opts []PhraseOption) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		p := &luxirpb.PhraseQuery{Field: field, Text: text, Words: words}
		for _, o := range opts {
			if o != nil {
				o(p)
			}
		}
		if p.Slop < 0 {
			return nil, queryErr("phrase", "%q slop must not be negative: %d", field, p.Slop)
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Phrase{Phrase: p}}, nil
	})
}

// FuzzyOption ;; edit a Fuzzy
type FuzzyOption func(*luxirpb.FuzzyQuery)

// MaxEdits ;; 0 to 2, left out Luxir picks by term length
func MaxEdits(n int) FuzzyOption {
	return func(f *luxirpb.FuzzyQuery) { v := clamp32(n); f.MaxEdits = &v }
}

// PrefixLength ;; leading bytes that must match exactly, left out is 1
func PrefixLength(n int) FuzzyOption {
	return func(f *luxirpb.FuzzyQuery) { v := clamp32(n); f.PrefixLength = &v }
}

// MaxExpansions ;; cap on terms tried, left out is 50
func MaxExpansions(n int) FuzzyOption {
	return func(f *luxirpb.FuzzyQuery) { f.MaxExpansions = clamp32(n) }
}

// Fuzzy ;; terms within a few edits of term
func Fuzzy(field, term string, opts ...FuzzyOption) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		f := &luxirpb.FuzzyQuery{Field: field, Term: term}
		for _, o := range opts {
			if o != nil {
				o(f)
			}
		}
		switch {
		case f.MaxEdits != nil && (*f.MaxEdits < 0 || *f.MaxEdits > 2):
			return nil, queryErr("fuzzy", "max edits must be 0 to 2: %d", *f.MaxEdits)
		case f.PrefixLength != nil && *f.PrefixLength < 0:
			return nil, queryErr("fuzzy", "prefix length must not be negative: %d", *f.PrefixLength)
		case f.MaxExpansions < 0:
			return nil, queryErr("fuzzy", "max expansions must not be negative: %d", f.MaxExpansions)
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Fuzzy{Fuzzy: f}}, nil
	})
}

// AnyOf ;; the field equals any of values, exact and constant score, one slice works as the values too
func AnyOf(field string, values ...any) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		// ;; a local copy, the query can be shared and built by several searches at once
		vals := values
		if len(vals) == 1 {
			switch vs := vals[0].(type) {
			case []any:
				vals = vs
			case []string, []int, []int64, []float32, []float64:
				val, _ := toVal(vs)
				return &luxirpb.Query{Kind: &luxirpb.Query_AnyOf{AnyOf: &luxirpb.AnyOfQuery{Field: field, Values: val}}}, nil
			}
		}
		for i, v := range vals {
			if v == nil {
				return nil, queryErr("any_of", "%q value %d is nil", field, i)
			}
		}
		val, err := toVal(vals)
		if err != nil {
			return nil, &QueryError{Query: "any_of", Err: err}
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_AnyOf{AnyOf: &luxirpb.AnyOfQuery{Field: field, Values: val}}}, nil
	})
}

// Bound ;; one side of an InRange
type Bound func(*luxirpb.RangeQuery) error

func bound(set func(*luxirpb.RangeQuery, *luxirpb.Val), v any) Bound {
	return func(r *luxirpb.RangeQuery) error {
		val, err := toVal(v)
		if err != nil {
			return err
		}
		set(r, val)
		return nil
	}
}

// Gte ;; at least v
func Gte(v any) Bound { return bound(func(r *luxirpb.RangeQuery, x *luxirpb.Val) { r.Gte = x }, v) }

// Gt ;; more than v
func Gt(v any) Bound { return bound(func(r *luxirpb.RangeQuery, x *luxirpb.Val) { r.Gt = x }, v) }

// Lte ;; at most v
func Lte(v any) Bound { return bound(func(r *luxirpb.RangeQuery, x *luxirpb.Val) { r.Lte = x }, v) }

// Lt ;; less than v
func Lt(v any) Bound { return bound(func(r *luxirpb.RangeQuery, x *luxirpb.Val) { r.Lt = x }, v) }

// InRange ;; the field falls between the bounds, no bounds is any value
//
// ;; dates take a time.Time or "2024-06", a bare "2024" is read as milliseconds
func InRange(field string, bounds ...Bound) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		r := &luxirpb.RangeQuery{Field: field}
		for _, b := range bounds {
			if b == nil {
				continue
			}
			if err := b(r); err != nil {
				return nil, &QueryError{Query: "range", Err: err}
			}
		}
		if r.Gte != nil && r.Gt != nil {
			return nil, queryErr("range", "%q sets both gte and gt", field)
		}
		if r.Lte != nil && r.Lt != nil {
			return nil, queryErr("range", "%q sets both lte and lt", field)
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Range{Range: r}}, nil
	})
}

// KnnOption ;; edit a Knn
type KnnOption func(*luxirpb.KnnQuery)

// Exact ;; true top k by scanning every vector
func Exact() KnnOption { return func(k *luxirpb.KnnQuery) { k.Exact = true } }

// RefineCandidates ;; approximate candidates rescored at full precision
func RefineCandidates(n int) KnnOption {
	return func(k *luxirpb.KnnQuery) { k.RefineCandidates = clamp32(n) }
}

func ivf(k *luxirpb.KnnQuery) *luxirpb.KnnQuery_Ivf {
	if k.Ivf == nil {
		k.Ivf = &luxirpb.KnnQuery_Ivf{}
	}
	return k.Ivf
}

// Nprobe ;; IVF lists to probe, 0 is adaptive
func Nprobe(n int) KnnOption { return func(k *luxirpb.KnnQuery) { ivf(k).Nprobe = clamp32(n) } }

// MinScanFraction ;; least fraction of vectors to scan, 0 to 1
func MinScanFraction(f float32) KnnOption {
	return func(k *luxirpb.KnnQuery) { ivf(k).MinScanFraction = f }
}

// Knn ;; the k documents nearest to vector
func Knn(field string, vector []float32, k int, opts ...KnnOption) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		switch {
		case k <= 0 || k > math.MaxInt32:
			return nil, queryErr("knn", "k must be positive: %d", k)
		case len(vector) == 0:
			return nil, queryErr("knn", "vector is empty")
		}
		for i, x := range vector {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, queryErr("knn", "vector[%d] is not finite", i)
			}
		}
		kq := &luxirpb.KnnQuery{
			Field: field,
			K:     int32(k),
			Query: &luxirpb.Vector{Kind: &luxirpb.Vector_F32{F32: &luxirpb.ArrFloat{V: vector}}},
		}
		for _, o := range opts {
			if o != nil {
				o(kq)
			}
		}
		switch f := kq.GetIvf().GetMinScanFraction(); {
		case kq.RefineCandidates < 0:
			return nil, queryErr("knn", "refine candidates must not be negative: %d", kq.RefineCandidates)
		case kq.GetIvf().GetNprobe() < 0:
			return nil, queryErr("knn", "nprobe must not be negative: %d", kq.GetIvf().GetNprobe())
		case !(f >= 0 && f <= 1):
			return nil, queryErr("knn", "min scan fraction must be in [0, 1]: %v", f)
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Knn{Knn: kq}}, nil
	})
}

// Point ;; a place, named so lat and lon can't be swapped
type Point struct{ Lat, Lon float64 }

// Box ;; bounding box in degrees, MinLon above MaxLon crosses the dateline
type Box struct{ MinLat, MaxLat, MinLon, MaxLon float64 }

func okLat(v float64) bool { return v >= -90 && v <= 90 }
func okLon(v float64) bool { return v >= -180 && v <= 180 }

// GeoBox ;; points inside the box
func GeoBox(field string, b Box) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		switch {
		case !okLat(b.MinLat) || !okLat(b.MaxLat):
			return nil, queryErr("geo_box", "latitude must be in [-90, 90]")
		case !okLon(b.MinLon) || !okLon(b.MaxLon):
			return nil, queryErr("geo_box", "longitude must be in [-180, 180]")
		case b.MinLat > b.MaxLat:
			return nil, queryErr("geo_box", "min lat %v is above max lat %v", b.MinLat, b.MaxLat)
		}
		g := &luxirpb.GeoBoxQuery{Field: field, MinLat: b.MinLat, MaxLat: b.MaxLat, MinLon: b.MinLon, MaxLon: b.MaxLon}
		return &luxirpb.Query{Kind: &luxirpb.Query_GeoBox{GeoBox: g}}, nil
	})
}

// GeoDistance ;; points within radiusMeters of center
func GeoDistance(field string, center Point, radiusMeters float64) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		switch {
		case !okLat(center.Lat) || !okLon(center.Lon):
			return nil, queryErr("geo_distance", "center must be lat [-90, 90], lon [-180, 180]")
		case radiusMeters < 0 || math.IsNaN(radiusMeters) || math.IsInf(radiusMeters, 0):
			return nil, queryErr("geo_distance", "radius must be finite and not negative: %v", radiusMeters)
		}
		g := &luxirpb.GeoDistanceQuery{Field: field, Lat: center.Lat, Lon: center.Lon, RadiusMeters: radiusMeters}
		return &luxirpb.Query{Kind: &luxirpb.Query_GeoDistance{GeoDistance: g}}, nil
	})
}

func boolProto(b *luxirpb.BooleanQuery) *luxirpb.Query {
	return &luxirpb.Query{Kind: &luxirpb.Query_Boolean{Boolean: b}}
}

// notQuery ;; Not, kept as a type so And can turn it into a prohibited clause
type notQuery struct{ q Query }

func (n notQuery) toProto() (*luxirpb.Query, error) {
	inner, err := protoOf("not", n.q)
	if err != nil {
		return nil, err
	}
	return boolProto(&luxirpb.BooleanQuery{Prohibited: []*luxirpb.Query{inner}}), nil
}

// Not ;; everything the query does not match
func Not(q Query) Query { return notQuery{q} }

// And ;; every query, scores add up, a Not here is a prohibited clause
func And(qs ...Query) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		if len(qs) == 0 {
			return nil, queryErr("and", "no queries")
		}
		b := &luxirpb.BooleanQuery{}
		for _, q := range qs {
			if n, ok := q.(notQuery); ok {
				inner, err := protoOf("not", n.q)
				if err != nil {
					return nil, err
				}
				b.Prohibited = append(b.Prohibited, inner)
				continue
			}
			pq, err := protoOf("and", q)
			if err != nil {
				return nil, err
			}
			b.Required = append(b.Required, pq)
		}
		return boolProto(b), nil
	})
}

// Or ;; any query, a Not here stays its own clause (everything except) so it can't prohibit the others
func Or(qs ...Query) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		if len(qs) == 0 {
			return nil, queryErr("or", "no queries")
		}
		b := &luxirpb.BooleanQuery{}
		for _, q := range qs {
			pq, err := protoOf("or", q)
			if err != nil {
				return nil, err
			}
			b.Optional = append(b.Optional, pq)
		}
		return boolProto(b), nil
	})
}

// boolSpec ;; what BoolOption edits
type boolSpec struct {
	must, should, mustNot, filter []Query
	minShould                     int
}

// BoolOption ;; one clause group of a Bool
type BoolOption func(*boolSpec)

// Must ;; required, adds to the score
func Must(qs ...Query) BoolOption { return func(b *boolSpec) { b.must = append(b.must, qs...) } }

// Should ;; optional, adds to the score
func Should(qs ...Query) BoolOption { return func(b *boolSpec) { b.should = append(b.should, qs...) } }

// MustNot ;; may not match
func MustNot(qs ...Query) BoolOption {
	return func(b *boolSpec) { b.mustNot = append(b.mustNot, qs...) }
}

// Filter ;; required, adds nothing to the score
func Filter(qs ...Query) BoolOption { return func(b *boolSpec) { b.filter = append(b.filter, qs...) } }

// MinShould ;; how many Should clauses have to match
func MinShould(n int) BoolOption { return func(b *boolSpec) { b.minShould = n } }

// Bool ;; And / Or / Not with full control of each clause kind
func Bool(opts ...BoolOption) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		var s boolSpec
		for _, o := range opts {
			if o != nil {
				o(&s)
			}
		}
		if len(s.must)+len(s.should)+len(s.mustNot)+len(s.filter) == 0 {
			return nil, queryErr("bool", "no clauses")
		}
		if s.minShould < 0 {
			return nil, queryErr("bool", "min should must not be negative: %d", s.minShould)
		}
		b := &luxirpb.BooleanQuery{MinMatch: clamp32(s.minShould)}
		for _, g := range []struct {
			qs  []Query
			dst *[]*luxirpb.Query
		}{{s.must, &b.Required}, {s.should, &b.Optional}, {s.mustNot, &b.Prohibited}, {s.filter, &b.Filter}} {
			for _, q := range g.qs {
				pq, err := protoOf("bool", q)
				if err != nil {
					return nil, err
				}
				*g.dst = append(*g.dst, pq)
			}
		}
		return boolProto(b), nil
	})
}

// Boost ;; multiply every score by factor
func Boost(q Query, factor float32) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		if factor < 0 || math.IsNaN(float64(factor)) || math.IsInf(float64(factor), 0) {
			return nil, queryErr("boost", "factor must be finite and not negative: %v", factor)
		}
		pq, err := protoOf("boost", q)
		if err != nil {
			return nil, err
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Boost{Boost: &luxirpb.BoostQuery{Query: pq, Boost: &factor}}}, nil
	})
}

// ConstantScore ;; every match gets this score
func ConstantScore(q Query, score float32) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		pq, err := protoOf("constant_score", q)
		if err != nil {
			return nil, err
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_ConstantScore{ConstantScore: &luxirpb.ConstantScoreQuery{Query: pq, Score: &score}}}, nil
	})
}

// Rescore ;; same matches, score replaced by a value expression
func Rescore(q Query, expr string, vars map[string]any) Query {
	return queryFunc(func() (*luxirpb.Query, error) {
		vs, err := toVals(vars)
		if err != nil {
			return nil, &QueryError{Query: "rescore", Err: err}
		}
		pq, err := protoOf("rescore", q)
		if err != nil {
			return nil, err
		}
		return &luxirpb.Query{Kind: &luxirpb.Query_Rescore{Rescore: &luxirpb.RescoreQuery{Query: pq, Expr: expr, Vars: vs}}}, nil
	})
}
