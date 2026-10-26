package luxir

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func bucketValues(f *Facet) []any {
	out := make([]any, len(f.Buckets))
	for i, b := range f.Buckets {
		out[i] = b.Value
	}
	return out
}

func bucketCounts(f *Facet) []int64 {
	out := make([]int64, len(f.Buckets))
	for i, b := range f.Buckets {
		out[i] = b.Count
	}
	return out
}

func facetSearch(t *testing.T, l *Luxir, coll string, q Query, opts ...SearchOption) *SearchResult {
	t.Helper()
	r, err := l.Search(timeout(t), coll, q, opts...)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return r
}

func TestOpsLive_FieldFacets(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0),
		WithOp("authors", FieldFacet("author_s", Missing())),
		WithOp("tags", FieldFacet("tags_ss", Missing())),
		WithOp("years", FieldFacet("year_i")),
		WithOp("dates", FieldFacet("pub")),
		WithOp("terms", FieldFacet("title_t")))

	if r.Found != 3 || len(r.Hits) != 0 {
		t.Errorf("found = %d, hits = %d, want analytics only: 3 and 0", r.Found, len(r.Hits))
	}

	authors := r.Ops.Facet("authors")
	if got := bucketValues(authors); !reflect.DeepEqual(got, []any{"Herbert", "Gibson"}) {
		t.Errorf("authors = %v, want count order [Herbert Gibson]", got)
	}
	if got := bucketCounts(authors); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("author counts = %v", got)
	}
	if authors.Missing == nil || *authors.Missing != 0 {
		t.Errorf("authors missing = %v, want pointer to 0", authors.Missing)
	}

	tags := r.Ops.Facet("tags")
	if got := bucketValues(tags); !reflect.DeepEqual(got, []any{"scifi", "classic"}) || tags.Buckets[0].Count != 2 {
		t.Errorf("tags = %+v: a document counts once per value", tags.Buckets)
	}
	if tags.Missing == nil || *tags.Missing != 1 {
		t.Errorf("tags missing = %v, want pointer to 1", tags.Missing)
	}

	if got := bucketValues(r.Ops.Facet("years")); !reflect.DeepEqual(got, []any{int64(1965), int64(1969), int64(1984)}) {
		t.Errorf("years = %v, want int64 buckets", got)
	}
	if v := r.Ops.Facet("dates").Buckets[0].Value; v != int64(time.Date(1965, 7, 1, 0, 0, 0, 0, time.UTC).UnixMilli()) {
		t.Errorf("first date = %v, want epoch millis", v)
	}
	if got := bucketValues(r.Ops.Facet("terms")); got[0] != "dune" || r.Ops.Facet("terms").Buckets[0].Count != 2 {
		t.Errorf("title terms = %v, want dune first with 2", got)
	}
}

func TestOpsLive_FieldFacetLimitsAndMinCount(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)
	gibson := Match("author_s", "Gibson")

	tests := []struct {
		name string
		op   Op
		q    Query
		want []any
	}{
		{"limit 1", FieldFacet("year_i", FacetLimit(1)), nil, []any{int64(1965)}},
		{"limit none", FieldFacet("year_i", FacetLimit(0)), nil, []any{}},
		{"limit all", FieldFacet("year_i", FacetLimit(-1)), nil, []any{int64(1965), int64(1969), int64(1984)}},
		{"default hides zero", FieldFacet("author_s"), gibson, []any{"Gibson"}},
		{"min count zero shows zero", FieldFacet("author_s", MinCount(0)), gibson, []any{"Gibson", "Herbert"}},
		{"min count 2", FieldFacet("author_s", MinCount(2)), nil, []any{"Herbert"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := facetSearch(t, l, coll, tt.q, WithLimit(0), WithOp("f", tt.op))
			if got := bucketValues(r.Ops.Facet("f")); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buckets = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpsLive_SortByMetric(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	for desc, want := range map[bool][]any{true: {"Gibson", "Herbert"}, false: {"Herbert", "Gibson"}} {
		r := facetSearch(t, l, coll, nil, WithLimit(0),
			WithOp("a", FieldFacet("author_s", SubOp("avg", Avg("price_d")), SortByMetric("avg", desc))))
		if got := bucketValues(r.Ops.Facet("a")); !reflect.DeepEqual(got, want) {
			t.Errorf("desc = %v: buckets = %v, want %v", desc, got, want)
		}
	}
}

func TestOpsLive_RangeFacets(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)
	ms := func(y int) int64 { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() }

	r := facetSearch(t, l, coll, nil, WithLimit(0),
		WithOp("decades", RangeFacet("year_i", 1960, 1990, Gap(10), Missing(), SubOp("sum", Sum("year_i")))),
		WithOp("nonzero", RangeFacet("year_i", 1960, 1990, Gap(10), MinCount(1))),
		WithOp("prices", RangeFacet("price_d", 8.0, 14.0, Gap(2.0))),
		WithOp("years", RangeFacet("pub", time.Date(1965, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1985, 1, 1, 0, 0, 0, 0, time.UTC), CalendarGap(10, luxirpb.CalendarGap_YEAR))),
		WithOp("iso", RangeFacet("pub", "1960-01", "1990-01", CalendarGap(10, luxirpb.CalendarGap_YEAR), FacetTimeZone("America/Denver"))))

	decades := r.Ops.Facet("decades")
	if got := bucketCounts(decades); !reflect.DeepEqual(got, []int64{2, 0, 1}) {
		t.Errorf("decade counts = %v, want zero-count buckets kept: [2 0 1]", got)
	}
	if got := decades.Buckets[0].Value; got != (BucketRange{int64(1960), int64(1970)}) {
		t.Errorf("first decade = %#v", got)
	}
	if v, ok := decades.Buckets[0].Ops.Int("sum"); !ok || v != 1965+1969 {
		t.Errorf("decade sum = %v, %v", v, ok)
	}
	if _, ok := decades.Buckets[1].Ops.Int("sum"); ok {
		t.Error("empty decade has a sum, want none")
	}
	if decades.Missing == nil || *decades.Missing != 0 {
		t.Errorf("decades missing = %v", decades.Missing)
	}

	if got := bucketCounts(r.Ops.Facet("nonzero")); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("nonzero counts = %v, want MinCount(1) to drop the empty bucket", got)
	}
	if got := r.Ops.Facet("prices").Buckets[0].Value; got != (BucketRange{8.0, 10.0}) {
		t.Errorf("first price bucket = %#v, want float ends", got)
	}
	if got := bucketCounts(r.Ops.Facet("years")); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("calendar year counts = %v", got)
	}
	if got := r.Ops.Facet("years").Buckets[0].Value; got != (BucketRange{ms(1965), ms(1975)}) {
		t.Errorf("calendar bucket = %#v, want epoch millis ends", got)
	}
	if n := len(r.Ops.Facet("iso").Buckets); n != 3 {
		t.Errorf("iso buckets = %d, want 3 decades", n)
	}
}

func TestOpsLive_QueryFacet(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0), WithOp("q", QueryFacet([]NamedQuery{
		Named("herbert", Match("author_s", "Herbert")),
		Named("dune", dune),
		Named("nobody", Match("author_s", "Nobody")),
		Named("old", InRange("year_i", Lt(1970))),
	}, SubOp("avg", Avg("price_d")))))

	f := r.Ops.Facet("q")
	if got := bucketValues(f); !reflect.DeepEqual(got, []any{"herbert", "dune", "nobody", "old"}) {
		t.Errorf("buckets = %v, want request order", got)
	}
	if got := bucketCounts(f); !reflect.DeepEqual(got, []int64{2, 2, 0, 2}) {
		t.Errorf("counts = %v, want overlapping buckets and a kept zero", got)
	}
	if _, ok := f.Buckets[2].Ops.Float("avg"); ok {
		t.Error("empty bucket has an average, want none")
	}
}

func TestOpsLive_Metrics(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0),
		WithOp("avg", Avg("price_d")), WithOp("sum", Sum("year_i")), WithOp("min", Min("year_i")),
		WithOp("max", Max("price_d")), WithOp("count", Count("year_i")),
		WithOp("scaled", Metric("sum(year_i) * $k", map[string]any{"k": 2})))

	if v, ok := r.Ops.Float("avg"); !ok || v < 10.16 || v > 10.17 {
		t.Errorf("avg = %v, %v", v, ok)
	}
	if v, ok := r.Ops.Int("sum"); !ok || v != 1965+1969+1984 {
		t.Errorf("sum = %v, %v, want an exact int64", v, ok)
	}
	if v, ok := r.Ops.Int("min"); !ok || v != 1965 {
		t.Errorf("min = %v, %v", v, ok)
	}
	if v, ok := r.Ops.Float("max"); !ok || v != 12 {
		t.Errorf("max = %v, %v", v, ok)
	}
	if v, ok := r.Ops.Int("count"); !ok || v != 3 {
		t.Errorf("count = %v, %v", v, ok)
	}
	if v, ok := r.Ops.Int("scaled"); !ok || v != 2*(1965+1969+1984) {
		t.Errorf("scaled = %v, %v", v, ok)
	}
}

func TestOpsLive_MetricOnNothing(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, Match("title_t", "nothingmatchesthis"), WithOp("avg", Avg("price_d")))

	if v, present := r.Ops["avg"]; !present || v != nil {
		t.Errorf("avg = %v, present = %v, want a nil result", v, present)
	}
	if _, ok := r.Ops.Float("avg"); ok {
		t.Error("Float ok on an empty domain, want false")
	}
}

func TestOpsLive_PerBucketDocs(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0), WithOp("a", FieldFacet("author_s",
		SubOp("best", Docs(nil, WithLimit(1), WithFields("id"), WithSort("price_d", true))))))

	f := r.Ops.Facet("a")
	herbert, gibson := f.Buckets[0].Ops.Docs("best"), f.Buckets[1].Ops.Docs("best")
	if herbert.Found != 2 || !reflect.DeepEqual(ids(herbert), []string{"b1"}) {
		t.Errorf("herbert best = found %d ids %v, want the pricier b1 of 2", herbert.Found, ids(herbert))
	}
	if gibson.Found != 1 || !reflect.DeepEqual(ids(gibson), []string{"b3"}) {
		t.Errorf("gibson best = found %d ids %v", gibson.Found, ids(gibson))
	}
}

func TestOpsLive_NestedFacets(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0),
		WithOp("a", FieldFacet("author_s", SubOp("tags", FieldFacet("tags_ss", Missing())))))

	herbert := r.Ops.Facet("a").Buckets[0].Ops.Facet("tags")
	if got := bucketValues(herbert); !reflect.DeepEqual(got, []any{"scifi", "classic"}) {
		t.Errorf("herbert tags = %v", got)
	}
	gibson := r.Ops.Facet("a").Buckets[1].Ops.Facet("tags")
	if len(gibson.Buckets) != 0 || gibson.Missing == nil || *gibson.Missing != 1 {
		t.Errorf("gibson tags = %+v missing %v, want no buckets and 1 missing", gibson.Buckets, gibson.Missing)
	}
}

func TestOpsLive_Select(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil,
		WithOp("tags", FieldFacet("tags_ss", Select("classic"))),
		WithOp("auth", FieldFacet("author_s")))

	if r.Found != 1 || !reflect.DeepEqual(ids(r), []string{"b1"}) {
		t.Errorf("found = %d ids = %v, want selection to narrow the hits to b1", r.Found, ids(r))
	}
	if got := bucketCounts(r.Ops.Facet("tags")); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("tag counts = %v, want the facet's own counts left alone", got)
	}
	if got := bucketValues(r.Ops.Facet("auth")); !reflect.DeepEqual(got, []any{"Herbert"}) {
		t.Errorf("author buckets = %v, want other facets narrowed", got)
	}
}

func TestOpsLive_SelectAllAndAbsent(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	all := facetSearch(t, l, coll, nil, WithOp("tags", FieldFacet("tags_ss", SelectAll("scifi", "classic"))))
	if !reflect.DeepEqual(ids(all), []string{"b1"}) {
		t.Errorf("select all ids = %v, want only the doc with both tags", ids(all))
	}

	absent := facetSearch(t, l, coll, nil, WithOp("tags", FieldFacet("tags_ss", Select("nope"))))
	if absent.Found != 0 {
		t.Errorf("found = %d, want 0 for a value nothing has", absent.Found)
	}
	var appended bool
	for _, b := range absent.Ops.Facet("tags").Buckets {
		appended = appended || (b.Value == "nope" && b.Count == 0)
	}
	if !appended {
		t.Errorf("buckets = %+v, want the selected value appended with 0", absent.Ops.Facet("tags").Buckets)
	}
}

func TestOpsLive_BucketValueGoesBackAsSelection(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	first := facetSearch(t, l, coll, nil, WithLimit(0), WithOp("a", FieldFacet("author_s")))
	pick := first.Ops.Facet("a").Buckets[1].Value

	r := facetSearch(t, l, coll, nil, WithOp("a", FieldFacet("author_s", Select(pick))))
	if r.Found != 1 || !reflect.DeepEqual(ids(r), []string{"b3"}) {
		t.Errorf("found = %d ids = %v, want the selected bucket's documents", r.Found, ids(r))
	}
}

func TestOpsLive_RangeSelect(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithOp("d", RangeFacet("year_i", 1960, 1990, Gap(10), Select(1980))))

	if r.Found != 1 || !reflect.DeepEqual(ids(r), []string{"b3"}) {
		t.Errorf("found = %d ids = %v, want the 1980s bucket", r.Found, ids(r))
	}
}

func TestOpsLive_MultiSelectNavigation(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0),
		WithFilterExcept(Match("author_s", "Gibson"), "authors"),
		WithOp("authors", FieldFacet("author_s")),
		WithOp("years", FieldFacet("year_i")))

	if got := bucketValues(r.Ops.Facet("authors")); !reflect.DeepEqual(got, []any{"Herbert", "Gibson"}) {
		t.Errorf("authors = %v, want the filter skipped so alternatives stay visible", got)
	}
	if got := bucketValues(r.Ops.Facet("years")); !reflect.DeepEqual(got, []any{int64(1984)}) {
		t.Errorf("years = %v, want the filter applied", got)
	}
	if r.Found != 1 {
		t.Errorf("found = %d, want the filter applied to the hits", r.Found)
	}
}

func TestOpsLive_SelectOnNestedRejected(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	_, err := l.Search(timeout(t), coll, nil,
		WithOp("a", FieldFacet("author_s", SubOp("t", FieldFacet("tags_ss", Select("scifi"))))))

	var se *SearchError
	if !errors.As(err, &se) || se.RequestFailed().GetKind() != luxirpb.Error_INVALID_REQUEST {
		t.Errorf("err = %v (%T), want *SearchError with INVALID_REQUEST from Luxir", err, err)
	}
}

func TestOpsLive_Domains(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, dune, WithLimit(0),
		WithOp("std", FieldFacet("author_s")),
		WithOp("all", FieldFacet("author_s", WithDomainQuery(All()))),
		WithOp("filtered", FieldFacet("author_s", WithDomainQuery(All()), WithDomainFilter(Match("author_s", "Gibson")))),
		WithOp("avg_all", InDomain(Avg("price_d"), WithDomainQuery(All()))),
		WithOp("avg_dune", Avg("price_d")))

	if got := bucketCounts(r.Ops.Facet("std")); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("std counts = %v", got)
	}
	if got := bucketValues(r.Ops.Facet("all")); !reflect.DeepEqual(got, []any{"Herbert", "Gibson"}) {
		t.Errorf("all = %v, want the search's query ignored", got)
	}
	if got := bucketValues(r.Ops.Facet("filtered")); !reflect.DeepEqual(got, []any{"Gibson"}) {
		t.Errorf("filtered = %v, want the domain filter applied", got)
	}
	if v, _ := r.Ops.Float("avg_dune"); v != 9.25 {
		t.Errorf("avg over the matches = %v, want 9.25", v)
	}
	if v, _ := r.Ops.Float("avg_all"); v < 10.16 || v > 10.17 {
		t.Errorf("avg over everything = %v, want ~10.17", v)
	}
}

func TestOpsLive_ApplyParentFilters(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0), WithFilter(InRange("year_i", Lt(1970))),
		WithOp("plain", FieldFacet("author_s", WithDomainQuery(All()))),
		WithOp("kept", FieldFacet("author_s", WithDomainQuery(All()), ApplyParentFilters())))

	if got := bucketCounts(r.Ops.Facet("plain")); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Errorf("plain counts = %v, want the parent filter dropped", got)
	}
	if got := bucketCounts(r.Ops.Facet("kept")); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("kept counts = %v, want the parent filter kept", got)
	}
}

func TestOpsLive_UnknownFieldIsEmptyNotAnError(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	r := facetSearch(t, l, coll, nil, WithLimit(0), WithOp("f", FieldFacet("nosuch_s")))

	if f := r.Ops.Facet("f"); f == nil || len(f.Buckets) != 0 {
		t.Errorf("facet = %+v, want an empty facet: Luxir reports no error for it", f)
	}
}

func TestOpsLive_ManyDocumentsWithBatching(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	docs := make([]Document, 1000)
	for i := range docs {
		docs[i] = Document{"id": fmt.Sprintf("d%04d", i), "n_i": i, "g_s": fmt.Sprintf("g%d", i%4)}
	}
	if _, err := l.Index(timeout(t), coll, docs, WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	r := facetSearch(t, l, coll, nil, WithLimit(-1), WithSort("n_i", false),
		WithOp("g", FieldFacet("g_s", SubOp("sum", Sum("n_i")), SubOp("top", Docs(nil, WithLimit(1), WithFields("id"), WithSort("n_i", true))))),
		WithOp("total", Sum("n_i")))

	if r.Found != 1000 || len(r.Hits) != 1000 {
		t.Fatalf("found = %d, hits = %d, want 1000 and 1000", r.Found, len(r.Hits))
	}
	for i, h := range r.Hits {
		if h.Doc["n_i"] != int64(i) {
			t.Fatalf("hit %d has n_i = %v, batches out of order or duplicated", i, h.Doc["n_i"])
		}
	}
	g := r.Ops.Facet("g")
	if got := bucketCounts(g); !reflect.DeepEqual(got, []int64{250, 250, 250, 250}) {
		t.Errorf("group counts = %v", got)
	}
	if v, ok := r.Ops.Int("total"); !ok || v != 999*1000/2 {
		t.Errorf("total = %v, %v", v, ok)
	}
	if top := g.Buckets[0].Ops.Docs("top"); top == nil || len(top.Hits) != 1 {
		t.Errorf("per-bucket top = %+v", top)
	}
}

func TestOpsLive_ConcurrentSearches(t *testing.T) {
	l := getClient(t)
	coll := seedRich(t, l)

	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			r, err := l.Search(timeout(t), coll, nil, WithLimit(0), WithOp("a", FieldFacet("author_s")), WithOp("s", Sum("year_i")))
			if err == nil && (r.Ops.Facet("a") == nil || len(r.Ops.Facet("a").Buckets) != 2) {
				err = fmt.Errorf("facet = %+v", r.Ops.Facet("a"))
			}
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
