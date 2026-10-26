package luxir

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

var (
	dune     = Match("title_t", "dune")
	messiah  = Match("title_t", "messiah")
	dunePB   = `match:{field:"title_t" val:{s:"dune"}}`
	messiaPB = `match:{field:"title_t" val:{s:"messiah"}}`
)

func TestQuery_Proto(t *testing.T) {
	tests := []struct {
		name string
		q    Query
		want string
	}{
		{"all", All(), `all:true`},
		{"exists", Exists("tags_ss"), `exists:{field:"tags_ss"}`},

		{"match", dune, dunePB},
		{"match int", Match("year_i", 1965), `match:{field:"year_i" val:{i:1965}}`},
		{"match time", Match("pub", time.UnixMilli(-139449600000)), `match:{field:"pub" val:{i:-139449600000}}`},
		{"match require all", Match("title_t", "dune messiah", RequireAll()), `match:{field:"title_t" val:{s:"dune messiah"} operator:AND}`},
		{"match min match", Match("title_t", "a b c", MinMatch(2)), `match:{field:"title_t" val:{s:"a b c"} min_match:2}`},
		{"match both", Match("title_t", "a b", RequireAll(), MinMatch(1)), `match:{field:"title_t" val:{s:"a b"} operator:AND min_match:1}`},

		{"simple", Simple("dune", "title_t", "body_t"), `simple_query:{q:"dune" fields:"title_t" fields:"body_t"}`},
		{"simple with", SimpleWith("dune", []string{"title_t"}, RequireAll(), MinMatch(2), AllowedFields("title_t", "year_i")),
			`simple_query:{q:"dune" fields:"title_t" operator:AND min_match:2 allowed_fields:"title_t" allowed_fields:"year_i"}`},
		{"simple with no opts", SimpleWith("dune", []string{"title_t"}), `simple_query:{q:"dune" fields:"title_t"}`},

		{"expr", Expr("title_t:dune", nil), `expr:{q:"title_t:dune"}`},
		{"expr vars", Expr("year_i:>=$y AND title_t:$t", map[string]any{"y": 1969, "t": "dune"}),
			`expr:{q:"year_i:>=$y AND title_t:$t" vars:{key:"y" value:{i:1969}} vars:{key:"t" value:{s:"dune"}}}`},
		{"expr list var", Expr("tag_s:=$t", map[string]any{"t": []string{"a", "b"}}),
			`expr:{q:"tag_s:=$t" vars:{key:"t" value:{arr_s:{v:"a" v:"b"}}}}`},

		{"prefix", Prefix("title_t", "neuro"), `prefix:{field:"title_t" prefix:"neuro"}`},
		{"prefix empty", Prefix("title_t", ""), `prefix:{field:"title_t"}`},
		{"wildcard", Wildcard("title_t", "d?ne*"), `wildcard:{field:"title_t" pattern:"d?ne*"}`},
		{"regex", Regex("title_t", "dun.|neuro.*"), `regex:{field:"title_t" pattern:"dun.|neuro.*"}`},

		{"phrase", Phrase("title_t", "dune messiah"), `phrase:{field:"title_t" text:"dune messiah"}`},
		{"phrase slop", Phrase("title_t", "dune messiah", Slop(2)), `phrase:{field:"title_t" text:"dune messiah" slop:2}`},
		{"phrase words", PhraseWords("title_t", []string{"dune", "messiah"}), `phrase:{field:"title_t" words:"dune" words:"messiah"}`},
		{"phrase words slop", PhraseWords("title_t", []string{"a", "b"}, Slop(1)), `phrase:{field:"title_t" words:"a" words:"b" slop:1}`},

		{"fuzzy", Fuzzy("title_t", "dyne"), `fuzzy:{field:"title_t" term:"dyne"}`},
		{"fuzzy zero edits", Fuzzy("title_t", "dyne", MaxEdits(0)), `fuzzy:{field:"title_t" term:"dyne" max_edits:0}`},
		{"fuzzy zero prefix", Fuzzy("title_t", "dyne", PrefixLength(0)), `fuzzy:{field:"title_t" term:"dyne" prefix_length:0}`},
		{"fuzzy all", Fuzzy("title_t", "dyne", MaxEdits(2), PrefixLength(2), MaxExpansions(10)),
			`fuzzy:{field:"title_t" term:"dyne" max_edits:2 prefix_length:2 max_expansions:10}`},

		{"any of", AnyOf("author_s", "Herbert", "Gibson"), `any_of:{field:"author_s" values:{arr:{v:{s:"Herbert"} v:{s:"Gibson"}}}}`},
		{"any of one", AnyOf("year_i", 1965), `any_of:{field:"year_i" values:{arr:{v:{i:1965}}}}`},
		{"any of none", AnyOf("author_s"), `any_of:{field:"author_s" values:{arr:{}}}`},
		{"any of mixed", AnyOf("x_s", "a", 1), `any_of:{field:"x_s" values:{arr:{v:{s:"a"} v:{i:1}}}}`},
		{"any of string slice", AnyOf("tags_ss", []string{"a", "b"}), `any_of:{field:"tags_ss" values:{arr_s:{v:"a" v:"b"}}}`},
		{"any of int slice", AnyOf("year_i", []int{1965}), `any_of:{field:"year_i" values:{arr_i:{v:1965}}}`},
		{"any of int64 slice", AnyOf("year_i", []int64{1965}), `any_of:{field:"year_i" values:{arr_i:{v:1965}}}`},
		{"any of float slices", AnyOf("p_d", []float64{1.5}), `any_of:{field:"p_d" values:{arr_d:{v:1.5}}}`},
		{"any of float32 slice", AnyOf("p_f", []float32{1.5}), `any_of:{field:"p_f" values:{arr_f:{v:1.5}}}`},
		{"any of any slice", AnyOf("x_s", []any{"a", 1}), `any_of:{field:"x_s" values:{arr:{v:{s:"a"} v:{i:1}}}}`},
		{"any of empty slice", AnyOf("x_s", []string{}), `any_of:{field:"x_s" values:{arr_s:{}}}`},

		{"range", InRange("year_i", Gte(1960), Lt(1980)), `range:{field:"year_i" gte:{i:1960} lt:{i:1980}}`},
		{"range gt lte", InRange("year_i", Gt(1960), Lte(1980)), `range:{field:"year_i" gt:{i:1960} lte:{i:1980}}`},
		{"range open", InRange("year_i", Gte(1960)), `range:{field:"year_i" gte:{i:1960}}`},
		{"range none", InRange("year_i"), `range:{field:"year_i"}`},
		{"range nil bound", InRange("year_i", nil, Gte(1)), `range:{field:"year_i" gte:{i:1}}`},
		{"range time", InRange("pub", Gte(time.UnixMilli(0)), Lt(time.UnixMilli(1000))), `range:{field:"pub" gte:{i:0} lt:{i:1000}}`},
		{"range string", InRange("pub", Gte("1969-01"), Lt("1984-01")), `range:{field:"pub" gte:{s:"1969-01"} lt:{s:"1984-01"}}`},
		{"range last bound wins", InRange("year_i", Gte(1), Gte(2)), `range:{field:"year_i" gte:{i:2}}`},

		{"knn", Knn("emb", []float32{1, 0, 0}, 5), `knn:{field:"emb" k:5 query:{f32:{v:1 v:0 v:0}}}`},
		{"knn exact", Knn("emb", []float32{1}, 1, Exact()), `knn:{field:"emb" k:1 exact:true query:{f32:{v:1}}}`},
		{"knn refine", Knn("emb", []float32{1}, 1, RefineCandidates(50)), `knn:{field:"emb" k:1 refine_candidates:50 query:{f32:{v:1}}}`},
		{"knn nprobe", Knn("emb", []float32{1}, 1, Nprobe(8)), `knn:{field:"emb" k:1 query:{f32:{v:1}} ivf:{nprobe:8}}`},
		{"knn ivf both", Knn("emb", []float32{1}, 1, Nprobe(8), MinScanFraction(0.5)), `knn:{field:"emb" k:1 query:{f32:{v:1}} ivf:{nprobe:8 min_scan_fraction:0.5}}`},

		{"geo box", GeoBox("loc", Box{MinLat: 40, MaxLat: 41, MinLon: -75, MaxLon: -73}), `geo_box:{field:"loc" min_lat:40 max_lat:41 min_lon:-75 max_lon:-73}`},
		{"geo box dateline", GeoBox("loc", Box{MinLat: -1, MaxLat: 1, MinLon: 179, MaxLon: -179}), `geo_box:{field:"loc" min_lat:-1 max_lat:1 min_lon:179 max_lon:-179}`},
		{"geo box edges", GeoBox("loc", Box{MinLat: -90, MaxLat: 90, MinLon: -180, MaxLon: 180}), `geo_box:{field:"loc" min_lat:-90 max_lat:90 min_lon:-180 max_lon:180}`},
		{"geo distance", GeoDistance("loc", Point{Lat: 40.7, Lon: -74}, 1000), `geo_distance:{field:"loc" lat:40.7 lon:-74 radius_meters:1000}`},
		{"geo distance zero", GeoDistance("loc", Point{}, 0), `geo_distance:{field:"loc"}`},

		{"and", And(dune, messiah), `boolean:{required:{` + dunePB + `} required:{` + messiaPB + `}}`},
		{"and not", And(dune, Not(messiah)), `boolean:{required:{` + dunePB + `} prohibited:{` + messiaPB + `}}`},
		{"and not not", And(Not(dune), Not(messiah)), `boolean:{prohibited:{` + dunePB + `} prohibited:{` + messiaPB + `}}`},
		{"and one", And(dune), `boolean:{required:{` + dunePB + `}}`},
		{"and nested", And(Or(dune, messiah), Match("a_s", "x")),
			`boolean:{required:{boolean:{optional:{` + dunePB + `} optional:{` + messiaPB + `}}} required:{match:{field:"a_s" val:{s:"x"}}}}`},
		{"or", Or(dune, messiah), `boolean:{optional:{` + dunePB + `} optional:{` + messiaPB + `}}`},
		{"or not stays nested", Or(dune, Not(messiah)), `boolean:{optional:{` + dunePB + `} optional:{boolean:{prohibited:{` + messiaPB + `}}}}`},
		{"not", Not(dune), `boolean:{prohibited:{` + dunePB + `}}`},
		{"not not", Not(Not(dune)), `boolean:{prohibited:{boolean:{prohibited:{` + dunePB + `}}}}`},
		{"not in and in not", Not(And(dune, Not(messiah))),
			`boolean:{prohibited:{boolean:{required:{` + dunePB + `} prohibited:{` + messiaPB + `}}}}`},

		{"bool", Bool(Must(dune), Should(messiah), MustNot(Match("a_s", "x")), Filter(Exists("year_i")), MinShould(1)),
			`boolean:{required:{` + dunePB + `} optional:{` + messiaPB + `} prohibited:{match:{field:"a_s" val:{s:"x"}}} filter:{exists:{field:"year_i"}} min_match:1}`},
		{"bool repeated group", Bool(Must(dune), Must(messiah)), `boolean:{required:{` + dunePB + `} required:{` + messiaPB + `}}`},
		{"bool filter only", Bool(Filter(dune)), `boolean:{filter:{` + dunePB + `}}`},

		{"boost", Boost(dune, 2), `boost:{query:{` + dunePB + `} boost:2}`},
		{"boost zero", Boost(dune, 0), `boost:{query:{` + dunePB + `} boost:0}`},
		{"constant score", ConstantScore(dune, 3), `constant_score:{query:{` + dunePB + `} score:3}`},
		{"constant score zero", ConstantScore(dune, 0), `constant_score:{query:{` + dunePB + `} score:0}`},
		{"rescore", Rescore(dune, "score * $k", map[string]any{"k": 2}),
			`rescore:{query:{` + dunePB + `} expr:"score * $k" vars:{key:"k" value:{i:2}}}`},
		{"rescore no vars", Rescore(dune, "score", nil), `rescore:{query:{` + dunePB + `} expr:"score"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertProto(t, queryProto(t, tt.q), wantQuery(t, tt.want))
		})
	}
}

func TestQuery_Errors(t *testing.T) {
	nan := math.NaN()
	inf := math.Inf(1)
	tests := []struct {
		name  string
		q     Query
		query string
	}{
		{"match negative min", Match("t", "x", MinMatch(-1)), "match"},
		{"match allowed fields", Match("t", "x", AllowedFields("a")), "match"},
		{"simple negative min", SimpleWith("x", []string{"t"}, MinMatch(-1)), "simple"},
		{"simple no fields", Simple("dune"), "simple"},
		{"expr bad var", Expr("t:$x", map[string]any{"x": struct{}{}}), "expr"},
		{"phrase negative slop", Phrase("t", "a b", Slop(-1)), "phrase"},
		{"phrase words negative slop", PhraseWords("t", []string{"a"}, Slop(-1)), "phrase"},
		{"fuzzy edits low", Fuzzy("t", "x", MaxEdits(-1)), "fuzzy"},
		{"fuzzy edits high", Fuzzy("t", "x", MaxEdits(3)), "fuzzy"},
		{"fuzzy prefix", Fuzzy("t", "x", PrefixLength(-1)), "fuzzy"},
		{"fuzzy expansions", Fuzzy("t", "x", MaxExpansions(-1)), "fuzzy"},
		{"any of nil", AnyOf("t", "a", nil), "any_of"},
		{"any of nil in slice", AnyOf("t", []any{"a", nil}), "any_of"},
		{"any of bad", AnyOf("t", struct{}{}), "any_of"},
		{"range gte gt", InRange("t", Gte(1), Gt(2)), "range"},
		{"range lte lt", InRange("t", Lte(1), Lt(2)), "range"},
		{"range bad bound", InRange("t", Gte(struct{}{})), "range"},
		{"knn k zero", Knn("t", []float32{1}, 0), "knn"},
		{"knn k negative", Knn("t", []float32{1}, -1), "knn"},
		{"knn k too big", Knn("t", []float32{1}, math.MaxInt32+1), "knn"},
		{"knn empty", Knn("t", nil, 1), "knn"},
		{"knn nan", Knn("t", []float32{1, float32(nan)}, 1), "knn"},
		{"knn inf", Knn("t", []float32{float32(inf)}, 1), "knn"},
		{"geo box lat", GeoBox("t", Box{MinLat: -91, MaxLat: 0}), "geo_box"},
		{"geo box lon", GeoBox("t", Box{MinLon: 0, MaxLon: 181}), "geo_box"},
		{"geo box flipped", GeoBox("t", Box{MinLat: 2, MaxLat: 1}), "geo_box"},
		{"geo box nan", GeoBox("t", Box{MinLat: nan}), "geo_box"},
		{"geo distance center lat", GeoDistance("t", Point{Lat: 91}, 1), "geo_distance"},
		{"geo distance center lon", GeoDistance("t", Point{Lon: -181}, 1), "geo_distance"},
		{"geo distance negative", GeoDistance("t", Point{}, -1), "geo_distance"},
		{"geo distance nan", GeoDistance("t", Point{}, nan), "geo_distance"},
		{"geo distance inf", GeoDistance("t", Point{}, inf), "geo_distance"},
		{"and empty", And(), "and"},
		{"and nil", And(dune, nil), "and"},
		{"or empty", Or(), "or"},
		{"or nil", Or(nil), "or"},
		{"not nil", Not(nil), "not"},
		{"and not nil", And(dune, Not(nil)), "not"},
		{"bool empty", Bool(), "bool"},
		{"bool nil", Bool(Must(nil)), "bool"},
		{"bool negative min should", Bool(Should(dune), MinShould(-1)), "bool"},
		{"knn negative refine", Knn("v", []float32{1}, 1, RefineCandidates(-1)), "knn"},
		{"knn negative nprobe", Knn("v", []float32{1}, 1, Nprobe(-1)), "knn"},
		{"knn scan fraction high", Knn("v", []float32{1}, 1, MinScanFraction(1.5)), "knn"},
		{"knn scan fraction negative", Knn("v", []float32{1}, 1, MinScanFraction(-0.1)), "knn"},
		{"knn scan fraction nan", Knn("v", []float32{1}, 1, MinScanFraction(float32(nan))), "knn"},
		{"boost nil", Boost(nil, 1), "boost"},
		{"boost negative", Boost(dune, -1), "boost"},
		{"boost nan", Boost(dune, float32(nan)), "boost"},
		{"boost inf", Boost(dune, float32(inf)), "boost"},
		{"constant nil", ConstantScore(nil, 1), "constant_score"},
		{"rescore nil", Rescore(nil, "score", nil), "rescore"},
		{"rescore bad var", Rescore(dune, "score", map[string]any{"x": struct{}{}}), "rescore"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := tt.q.toProto()
			if err == nil {
				t.Fatalf("toProto = %v, want an error", pq)
			}
			var qe *QueryError
			if !errors.As(err, &qe) || qe.Query != tt.query {
				t.Errorf("err = %v (%T), want *QueryError for %q", err, err, tt.query)
			}
		})
	}
}

func TestQuery_ErrorKeepsCause(t *testing.T) {
	bad := Match("t", struct{}{})

	for name, q := range map[string]Query{
		"and": And(bad), "or": Or(bad), "not": Not(bad), "bool": Bool(Must(bad)),
		"boost": Boost(bad, 1), "constant": ConstantScore(bad, 1), "rescore": Rescore(bad, "score", nil),
	} {
		_, err := q.toProto()
		var me *MatchError
		if !errors.As(err, &me) {
			t.Errorf("%s: err = %v (%T), want the *MatchError cause kept", name, err, err)
		}
	}
}

func TestQuery_ThroughSearchError(t *testing.T) {
	l := &Luxir{}

	_, err := l.Search(timeout(t), "c", Knn("emb", nil, 1))

	var se *SearchError
	var qe *QueryError
	if !errors.As(err, &se) || !errors.As(err, &qe) {
		t.Fatalf("err = %v (%T), want *SearchError wrapping *QueryError", err, err)
	}
}

func seedRich(t *testing.T, l *Luxir) string {
	t.Helper()
	coll := newCollection(t, l)

	def := NewSchema().
		Date("pub", Range()).
		Geo("loc", Range()).
		Vector("emb", 3, luxirpb.VectorMetric_COSINE).
		MustBuild()
	if err := l.CreateCollection(timeout(t), coll, def); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	day := func(y int) time.Time { return time.Date(y, 7, 1, 0, 0, 0, 0, time.UTC) }
	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "b1", "title_t": "Dune", "year_i": 1965, "price_d": 10.5, "author_s": "Herbert", "tags_ss": []string{"scifi", "classic"},
			"pub": day(1965), "emb": []float32{1, 0, 0}, "loc": []float64{-74, 40.7}},
		{"id": "b2", "title_t": "Dune Messiah", "year_i": 1969, "price_d": 8.0, "author_s": "Herbert", "tags_ss": []string{"scifi"},
			"pub": day(1969), "emb": []float32{0.9, 0.1, 0}, "loc": []float64{179.5, 0}},
		{"id": "b3", "title_t": "Neuromancer", "year_i": 1984, "price_d": 12.0, "author_s": "Gibson",
			"pub": day(1984), "emb": []float32{0, 1, 0}, "loc": []float64{-179.5, 0}},
	}, WithCommit())
	if err != nil {
		t.Fatalf("seedRich Index: %v", err)
	}
	return coll
}

func sortedIDs(r *SearchResult) []string {
	out := ids(r)
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func TestQuery_Live(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)
	d := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }

	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"all", All(), []string{"b1", "b2", "b3"}},
		{"exists", Exists("tags_ss"), []string{"b1", "b2"}},

		{"not", Not(dune), []string{"b3"}},
		{"not not", Not(Not(dune)), []string{"b1", "b2"}},
		{"and", And(dune, Match("author_s", "Herbert")), []string{"b1", "b2"}},
		{"and not", And(Match("author_s", "Herbert"), Not(messiah)), []string{"b1"}},
		{"and not not", And(Not(dune), Not(Match("title_t", "neuromancer"))), nil},
		{"or", Or(Match("title_t", "neuromancer"), messiah), []string{"b2", "b3"}},
		{"or not", Or(Match("title_t", "neuromancer"), Not(dune)), []string{"b3"}},
		{"or not other", Or(messiah, Not(dune)), []string{"b2", "b3"}},
		{"not and", Not(And(dune, Not(messiah))), []string{"b2", "b3"}},

		{"match require all", Match("title_t", "dune messiah", RequireAll()), []string{"b2"}},
		{"match any", Match("title_t", "dune messiah"), []string{"b1", "b2"}},
		{"match min match", Match("title_t", "dune messiah neuromancer", MinMatch(2)), []string{"b2"}},
		{"simple with", SimpleWith("dune messiah", []string{"title_t"}, RequireAll()), []string{"b2"}},
		{"simple degrades", Simple(`"dune`, "title_t"), []string{"b1", "b2"}},

		{"prefix", Prefix("title_t", "neuro"), []string{"b3"}},
		{"wildcard", Wildcard("title_t", "d?ne"), []string{"b1", "b2"}},
		{"regex", Regex("title_t", "dun.|neuro.*"), []string{"b1", "b2", "b3"}},
		{"regex anchored", Regex("title_t", "dun"), nil},
		{"fuzzy", Fuzzy("title_t", "dyne", MaxEdits(1)), []string{"b1", "b2"}},
		{"fuzzy exact", Fuzzy("title_t", "dyne", MaxEdits(0)), nil},
		{"fuzzy prefix", Fuzzy("title_t", "dyne", MaxEdits(1), PrefixLength(2)), nil},
		{"phrase", Phrase("title_t", "dune messiah"), []string{"b2"}},
		{"phrase reversed", Phrase("title_t", "messiah dune"), nil},
		{"phrase slop", Phrase("title_t", "messiah dune", Slop(2)), []string{"b2"}},
		{"phrase words", PhraseWords("title_t", []string{"dune", "messiah"}), []string{"b2"}},

		{"any of", AnyOf("author_s", "Gibson", "Nobody"), []string{"b3"}},
		{"any of none", AnyOf("author_s"), nil},
		{"any of ints", AnyOf("year_i", 1965, 1984), []string{"b1", "b3"}},
		{"any of a slice", AnyOf("tags_ss", []string{"classic", "nope"}), []string{"b1"}},

		{"range ints", InRange("year_i", Gte(1965), Lt(1984)), []string{"b1", "b2"}},
		{"range open", InRange("year_i", Gt(1965)), []string{"b2", "b3"}},
		{"range lte", InRange("year_i", Lte(1965)), []string{"b1"}},
		{"range any value", InRange("tags_ss"), []string{"b1", "b2"}},
		{"range time", InRange("pub", Gte(d(1969)), Lt(d(1984))), []string{"b2"}},
		{"range iso partial", InRange("pub", Gte("1969-01"), Lt("1984-01")), []string{"b2"}},
		{"range bare year is millis", InRange("pub", Gte("1969")), []string{"b3"}},

		{"expr", Expr("year_i:>=1969 AND NOT title_t:messiah", nil), []string{"b3"}},
		{"expr vars", Expr("year_i:>=$y AND title_t:$t", map[string]any{"y": 1969, "t": "dune"}), []string{"b2"}},
		{"expr var is not syntax", Expr("title_t:$t", map[string]any{"t": "author_s:Gibson"}), nil},
		{"expr in simple", Expr("simple_query($q, fields=[title_t])", map[string]any{"q": "neuro*"}), []string{"b3"}},

		{"knn", Knn("emb", []float32{1, 0, 0}, 2), []string{"b1", "b2"}},
		{"knn exact", Knn("emb", []float32{0, 1, 0}, 1, Exact()), []string{"b3"}},
		{"knn filtered", And(Knn("emb", []float32{1, 0, 0}, 3), Match("author_s", "Gibson")), []string{"b3"}},

		{"geo box", GeoBox("loc", Box{MinLat: 40, MaxLat: 41, MinLon: -75, MaxLon: -73}), []string{"b1"}},
		{"geo dateline", GeoBox("loc", Box{MinLat: -1, MaxLat: 1, MinLon: 179, MaxLon: -179}), []string{"b2", "b3"}},
		{"geo distance", GeoDistance("loc", Point{Lat: 40.7, Lon: -74}, 1000), []string{"b1"}},
		{"geo distance far", GeoDistance("loc", Point{Lat: 0, Lon: 180}, 200000), []string{"b2", "b3"}},

		{"bool", Bool(Must(dune), MustNot(messiah), Filter(InRange("year_i", Lt(1970)))), []string{"b1"}},
		{"bool min should", Bool(Should(dune, Match("author_s", "Herbert")), MinShould(2)), []string{"b1", "b2"}},
		{"bool should only", Bool(Should(messiah, Match("title_t", "neuromancer"))), []string{"b2", "b3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := l.Search(timeout(t), coll, tt.q)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := sortedIDs(r); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuery_LiveScores(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	score := func(q Query) map[string]float32 {
		r, err := l.Search(timeout(t), coll, q)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		out := map[string]float32{}
		for _, h := range r.Hits {
			out[h.Doc["id"].(string)] = h.Score
		}
		return out
	}
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

	base := score(dune)
	for id, s := range score(Boost(dune, 2)) {
		if !near(s, base[id]*2) {
			t.Errorf("boost: %s score = %v, want %v", id, s, base[id]*2)
		}
	}
	for id, s := range score(Boost(dune, 0)) {
		if s != 0 {
			t.Errorf("boost 0: %s score = %v, want 0", id, s)
		}
	}
	for id, s := range score(ConstantScore(dune, 3)) {
		if !near(s, 3) {
			t.Errorf("constant score: %s score = %v, want 3", id, s)
		}
	}
	for id, s := range score(Rescore(dune, "1 + $k", map[string]any{"k": 4})) {
		if !near(s, 5) {
			t.Errorf("rescore: %s score = %v, want 5", id, s)
		}
	}

	r, err := l.Search(timeout(t), coll, Knn("emb", []float32{1, 0, 0}, 3))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"b1", "b2", "b3"}) {
		t.Errorf("knn order = %v, want nearest first [b1 b2 b3]", got)
	}
}

func TestQuery_LiveRejects(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	for name, q := range map[string]Query{
		"unknown field":  Prefix("nosuffix", "x"),
		"missing var":    Expr("year_i:>=$y", nil),
		"knn wrong dims": Knn("emb", []float32{1, 0}, 1),
		"bad coercion":   InRange("year_i", Gte("abc")),
	} {
		_, err := l.Search(timeout(t), coll, q)

		var se *SearchError
		if !errors.As(err, &se) || se.RequestFailed() == nil {
			t.Errorf("%s: err = %v (%T), want *SearchError carrying Luxir's error", name, err, err)
		}
	}
}

// TestAnyOf_SharedQuery ;; one query built by many searches at once (run with -race)
func TestAnyOf_SharedQuery(t *testing.T) {
	q := AnyOf("tags_ss", []any{"scifi", "classic"})
	want := wantQuery(t, `any_of:{field:"tags_ss" values:{arr:{v:{s:"scifi"} v:{s:"classic"}}}}`)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 50 {
				pq, err := q.toProto()
				if err != nil || !proto.Equal(pq, want) {
					t.Errorf("toProto = %v, %v", pq, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestHugeInts_Saturate ;; an int beyond int32 saturates instead of wrapping (1<<32 used to become 0)
func TestHugeInts_Saturate(t *testing.T) {
	if strconv.IntSize == 32 {
		t.Skip("int is int32")
	}
	var wide int64 = 1 << 32
	huge, neg := int(wide), -int(wide)

	if _, err := Fuzzy("t", "x", MaxEdits(huge)).toProto(); err == nil {
		t.Error("MaxEdits(1<<32) passed the 0 to 2 check")
	}
	for name, q := range map[string]Query{
		"Slop":         Phrase("t", "a b", Slop(neg)),
		"MinMatch":     Match("t", "a", MinMatch(neg)),
		"MinShould":    Bool(Should(All()), MinShould(neg)),
		"PrefixLength": Fuzzy("t", "x", PrefixLength(neg)),
	} {
		if _, err := q.toProto(); err == nil {
			t.Errorf("%s(-1<<32) passed the negative check", name)
		}
	}

	max32 := `2147483647`
	for name, tt := range map[string]struct {
		q    Query
		want string
	}{
		"Slop":             {Phrase("t", "a b", Slop(huge)), `phrase:{field:"t" text:"a b" slop:` + max32 + `}`},
		"MinMatch":         {Match("t", "a", MinMatch(huge)), `match:{field:"t" val:{s:"a"} min_match:` + max32 + `}`},
		"SimpleWith":       {SimpleWith("a", []string{"t"}, MinMatch(huge)), `simple_query:{q:"a" fields:"t" min_match:` + max32 + `}`},
		"MinShould":        {Bool(Should(All()), MinShould(huge)), `boolean:{optional:{all:true} min_match:` + max32 + `}`},
		"PrefixLength":     {Fuzzy("t", "x", PrefixLength(huge), MaxExpansions(huge)), `fuzzy:{field:"t" term:"x" prefix_length:` + max32 + ` max_expansions:` + max32 + `}`},
		"RefineCandidates": {Knn("v", []float32{1}, 1, RefineCandidates(huge), Nprobe(huge)), `knn:{field:"v" k:1 query:{f32:{v:1}} refine_candidates:` + max32 + ` ivf:{nprobe:` + max32 + `}}`},
	} {
		pq, err := tt.q.toProto()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		assertProto(t, pq, wantQuery(t, tt.want))
	}

	req := searchRequest(t, WithBatchSize(huge), WithOp("m", RangeFacet("d", 0, 1, CalendarGap(huge, luxirpb.CalendarGap_DAY))))
	top := req.GetOps()[opName].GetTopDocs()
	if top.GetBatchSize() != math.MaxInt32 || top.GetOps()["m"].GetRangeFacet().GetCalendarGap().GetN() != math.MaxInt32 {
		t.Errorf("batch size, calendar gap = %d, %d, want both saturated", top.GetBatchSize(), top.GetOps()["m"].GetRangeFacet().GetCalendarGap().GetN())
	}
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	if _, err := l.Fuse(timeout(t), "c", []Source{FuseSource("a", nil)}, WithRRF(huge)); err != nil {
		t.Fatal(err)
	}
	if k := f.last(t).GetOps()[opName].GetFusion().GetRrf().GetK(); k != math.MaxInt32 {
		t.Errorf("rrf k = %d, want saturated", k)
	}
}
