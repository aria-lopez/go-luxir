package luxir

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func opEntry(name, value string) string {
	return `ops:{key:"` + name + `" value:{` + value + `}}`
}

func TestOps_Request(t *testing.T) {
	avgPrice := `expr_op:{expr:"avg(price_d)"}`
	tests := []struct {
		name string
		op   Op
		want string
	}{
		{"field facet", FieldFacet("author_s"), `field_facet:{field:"author_s"}`},
		{"field facet limit", FieldFacet("a", FacetLimit(10)), `field_facet:{field:"a" limit:10}`},
		{"field facet limit all", FieldFacet("a", FacetLimit(-1)), `field_facet:{field:"a" limit:-1}`},
		{"field facet limit none", FieldFacet("a", FacetLimit(0)), `field_facet:{field:"a" limit:0}`},
		{"field facet mincount zero", FieldFacet("a", MinCount(0)), `field_facet:{field:"a" mincount:0}`},
		{"field facet mincount", FieldFacet("a", MinCount(3)), `field_facet:{field:"a" mincount:3}`},
		{"field facet missing", FieldFacet("a", Missing()), `field_facet:{field:"a" missing:true}`},
		{"field facet sort", FieldFacet("a", SubOp("m", Avg("p")), SortByMetric("m", true)),
			`field_facet:{field:"a" sort:{expr:"m" dir:DESC} ` + opEntry("m", `expr_op:{expr:"avg(p)"}`) + `}`},
		{"field facet sort asc", FieldFacet("a", SubOp("m", Avg("p")), SortByMetric("m", false)),
			`field_facet:{field:"a" sort:{expr:"m" dir:ASC} ` + opEntry("m", `expr_op:{expr:"avg(p)"}`) + `}`},
		{"field facet select", FieldFacet("a", Select("scifi", "classic")),
			`field_facet:{field:"a" selected:{arr:{v:{s:"scifi"} v:{s:"classic"}}}}`},
		{"field facet select all", FieldFacet("a", SelectAll("scifi")),
			`field_facet:{field:"a" selected:{arr:{v:{s:"scifi"}}} selection_mode:ALL}`},
		{"field facet select ints", FieldFacet("a", Select(1965)), `field_facet:{field:"a" selected:{arr:{v:{i:1965}}}}`},
		{"field facet select none", FieldFacet("a", Select()), `field_facet:{field:"a" selected:{arr:{}}}`},
		{"field facet sub ops", FieldFacet("a", SubOp("m", Avg("p")), SubOp("n", Sum("p"))),
			`field_facet:{field:"a" ` + opEntry("m", `expr_op:{expr:"avg(p)"}`) + ` ` + opEntry("n", `expr_op:{expr:"sum(p)"}`) + `}`},

		{"range facet", RangeFacet("year_i", 1960, 2000, Gap(10)), `range_facet:{field:"year_i" start:{i:1960} end:{i:2000} gap:{i:10}}`},
		{"range facet float", RangeFacet("p", 0.5, 9.5, Gap(1.5)), `range_facet:{field:"p" start:{d:0.5} end:{d:9.5} gap:{d:1.5}}`},
		{"range facet times", RangeFacet("pub", time.UnixMilli(0), time.UnixMilli(1000), Gap(100)), `range_facet:{field:"pub" start:{i:0} end:{i:1000} gap:{i:100}}`},
		{"range facet iso", RangeFacet("pub", "1960-01", "1970-01", CalendarGap(1, luxirpb.CalendarGap_MONTH)),
			`range_facet:{field:"pub" start:{s:"1960-01"} end:{s:"1970-01"} calendar_gap:{n:1 unit:MONTH}}`},
		{"range facet zone", RangeFacet("pub", 0, 1, CalendarGap(2, luxirpb.CalendarGap_WEEK), FacetTimeZone("America/Denver")),
			`range_facet:{field:"pub" start:{i:0} end:{i:1} calendar_gap:{n:2 unit:WEEK} time_zone:"America/Denver"}`},
		{"range facet options", RangeFacet("y", 0, 10, Gap(5), Missing(), MinCount(1)),
			`range_facet:{field:"y" start:{i:0} end:{i:10} gap:{i:5} missing:true mincount:1}`},
		{"range facet select", RangeFacet("y", 0, 10, Gap(5), Select(5)),
			`range_facet:{field:"y" start:{i:0} end:{i:10} gap:{i:5} selected:{arr:{v:{i:5}}}}`},
		{"range facet sub op", RangeFacet("y", 0, 10, Gap(5), SubOp("n", Sum("y"))),
			`range_facet:{field:"y" start:{i:0} end:{i:10} gap:{i:5} ` + opEntry("n", `expr_op:{expr:"sum(y)"}`) + `}`},

		{"query facet", QueryFacet([]NamedQuery{Named("old", InRange("y", Lt(1970))), Named("new", InRange("y", Gte(1970)))}),
			`query_facet:{buckets:{name:"old" query:{range:{field:"y" lt:{i:1970}}}} buckets:{name:"new" query:{range:{field:"y" gte:{i:1970}}}}}`},
		{"query facet select", QueryFacet([]NamedQuery{Named("a", dune)}, SelectAll("a")),
			`query_facet:{buckets:{name:"a" query:{` + dunePB + `}} selected:{arr:{v:{s:"a"}}} selection_mode:ALL}`},

		{"avg", Avg("price_d"), avgPrice},
		{"sum", Sum("year_i"), `expr_op:{expr:"sum(year_i)"}`},
		{"min", Min("year_i"), `expr_op:{expr:"min(year_i)"}`},
		{"max", Max("year_i"), `expr_op:{expr:"max(year_i)"}`},
		{"count", Count("year_i"), `expr_op:{expr:"sum(count(year_i))"}`},
		{"metric", Metric("avg(p) * $k", map[string]any{"k": 2}), `expr_op:{expr:"avg(p) * $k" vars:{key:"k" value:{i:2}}}`},

		{"docs", Docs(nil), `top_docs:{get_number:true get_scores:true document_format:ROWS}`},
		{"docs options", Docs(dune, WithLimit(2), WithFields("id"), WithSort("y", true), WithFilter(messiah), WithOffset(1), WithCount(false), WithScores(false), WithBatchSize(5)),
			`top_docs:{query:{` + dunePB + `} limit:2 offset:1 fields:"id" batch_size:5 sort:{expr:"y" dir:DESC} filter:{query:{` + messiaPB + `}} document_format:ROWS}`},
		{"docs with ops", Docs(nil, WithOp("m", Avg("p"))),
			`top_docs:{get_number:true get_scores:true document_format:ROWS ` + opEntry("m", `expr_op:{expr:"avg(p)"}`) + `}`},
		{"docs filter except", Docs(nil, WithFilterExcept(dune, "x")),
			`top_docs:{get_number:true get_scores:true document_format:ROWS filter:{query:{` + dunePB + `} except_ops:"x"}}`},

		{"facet per bucket docs", FieldFacet("a", SubOp("best", Docs(nil, WithLimit(1), WithFields("id")))),
			`field_facet:{field:"a" ` + opEntry("best", `top_docs:{limit:1 fields:"id" get_number:true get_scores:true document_format:ROWS}`) + `}`},
		{"facet nested three deep", FieldFacet("a", SubOp("b", FieldFacet("b", SubOp("c", Avg("p"))))),
			`field_facet:{field:"a" ` + opEntry("b", `field_facet:{field:"b" `+opEntry("c", `expr_op:{expr:"avg(p)"}`)+`}`) + `}`},

		{"domain query", FieldFacet("a", WithDomainQuery(All())), `field_facet:{field:"a"} domain:{query:{all:true}}`},
		{"domain filter", FieldFacet("a", WithDomainFilter(dune, messiah)),
			`field_facet:{field:"a"} domain:{filter:{` + dunePB + `} filter:{` + messiaPB + `}}`},
		{"domain parent filters", FieldFacet("a", WithDomainQuery(dune), ApplyParentFilters()),
			`field_facet:{field:"a"} domain:{query:{` + dunePB + `} apply_parent_filters:true}`},
		{"in domain metric", InDomain(Avg("price_d"), WithDomainQuery(All())), avgPrice + ` domain:{query:{all:true}}`},
		{"in domain docs", InDomain(Docs(nil, WithLimit(1)), WithDomainFilter(dune)),
			`top_docs:{limit:1 get_number:true get_scores:true document_format:ROWS} domain:{filter:{` + dunePB + `}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchRequest(t, WithOp("a", tt.op))
			assertProto(t, got, wantRequest(t, hitsRequest(`get_number:true `+opEntry("a", tt.want), ``)))
		})
	}
}

func TestOps_ManyAtOnce(t *testing.T) {
	got := searchRequest(t, WithOp("z", Avg("p")), WithOp("a", FieldFacet("x_s")))

	want := wantRequest(t, hitsRequest(`get_number:true `+opEntry("a", `field_facet:{field:"x_s"}`)+` `+opEntry("z", `expr_op:{expr:"avg(p)"}`), ``))
	assertProto(t, got, want)
}

func TestOps_Errors(t *testing.T) {
	l := &Luxir{}
	ctx := context.Background()

	tests := []struct {
		name string
		op   Op
		want string
	}{
		{"nil op", nil, "needs a name and an op"},
		{"range no gap", RangeFacet("y", 0, 10), "exactly one of Gap or CalendarGap"},
		{"range both gaps", RangeFacet("y", 0, 10, Gap(1), CalendarGap(1, luxirpb.CalendarGap_DAY)), "exactly one of Gap or CalendarGap"},
		{"range no start", RangeFacet("y", nil, 10, Gap(1)), "needs a start and an end"},
		{"range no end", RangeFacet("y", 0, nil, Gap(1)), "needs a start and an end"},
		{"range bad start", RangeFacet("y", struct{}{}, 10, Gap(1)), "start/end"},
		{"range bad gap", RangeFacet("y", 0, 10, Gap(struct{}{})), "gap"},
		{"range bad selected", RangeFacet("y", 0, 10, Gap(1), Select(struct{}{})), "selected"},
		{"field bad selected", FieldFacet("a", Select(struct{}{})), "selected"},
		{"query facet empty", QueryFacet(nil), "at least one bucket"},
		{"query facet empty name", QueryFacet([]NamedQuery{Named("", dune)}), "unique name"},
		{"query facet dup name", QueryFacet([]NamedQuery{Named("a", dune), Named("a", messiah)}), "unique name"},
		{"query facet nil query", QueryFacet([]NamedQuery{Named("a", nil)}), "query is nil"},
		{"query facet bad query", QueryFacet([]NamedQuery{Named("a", Knn("e", nil, 1))}), "vector is empty"},
		{"metric bad var", Metric("x", map[string]any{"v": struct{}{}}), "var"},
		{"parent filters alone", FieldFacet("a", ApplyParentFilters()), "ApplyParentFilters needs WithDomainQuery"},
		{"domain bad query", FieldFacet("a", WithDomainQuery(Knn("e", nil, 1))), "vector is empty"},
		{"domain bad filter", FieldFacet("a", WithDomainFilter(nil)), "query is nil"},
		{"in domain nil", InDomain(nil, WithDomainQuery(All())), "needs an op"},
		{"in domain twice", InDomain(FieldFacet("a", WithDomainQuery(All())), WithDomainQuery(All())), "already has a domain"},
		{"in domain wrong option", InDomain(Avg("p"), FacetLimit(1)), "FacetLimit does not apply to InDomain"},
		{"sub op twice", FieldFacet("a", SubOp("m", Avg("p")), SubOp("m", Sum("p"))), "defined twice"},
		{"sub op empty name", FieldFacet("a", SubOp("", Avg("p"))), "needs a name and an op"},
		{"sub op nil", FieldFacet("a", SubOp("m", nil)), "needs a name and an op"},
		{"sub op bad", FieldFacet("a", SubOp("m", RangeFacet("y", 0, 1))), "exactly one of Gap or CalendarGap"},
		{"docs bad query", Docs(Knn("e", nil, 1)), "vector is empty"},
		{"docs bad filter", Docs(nil, WithFilter(Knn("e", nil, 1))), "vector is empty"},
		{"fusion no sources", Fusion(nil), "at least one source"},
		{"fusion empty source name", Fusion([]Source{FuseSource("", dune)}), "unique name"},
		{"fusion dup source name", Fusion([]Source{FuseSource("a", dune), FuseSource("a", messiah)}), "unique name"},
		{"fusion bad source query", Fusion([]Source{FuseSource("a", Knn("e", nil, 1))}), "vector is empty"},

		{"limit on range", RangeFacet("y", 0, 1, Gap(1), FacetLimit(1)), "FacetLimit does not apply"},
		{"limit on query", QueryFacet([]NamedQuery{Named("a", dune)}, FacetLimit(1)), "FacetLimit does not apply"},
		{"mincount on query", QueryFacet([]NamedQuery{Named("a", dune)}, MinCount(1)), "MinCount does not apply"},
		{"missing on query", QueryFacet([]NamedQuery{Named("a", dune)}, Missing()), "Missing does not apply"},
		{"sort on range", RangeFacet("y", 0, 1, Gap(1), SortByMetric("m", true)), "SortByMetric does not apply"},
		{"sort on query", QueryFacet([]NamedQuery{Named("a", dune)}, SortByMetric("m", true)), "SortByMetric does not apply"},
		{"gap on field", FieldFacet("a", Gap(1)), "Gap does not apply"},
		{"gap on query", QueryFacet([]NamedQuery{Named("a", dune)}, Gap(1)), "Gap does not apply"},
		{"calendar gap on field", FieldFacet("a", CalendarGap(1, luxirpb.CalendarGap_DAY)), "CalendarGap does not apply"},
		{"zone on field", FieldFacet("a", FacetTimeZone("UTC")), "FacetTimeZone does not apply"},
		{"zone on query", QueryFacet([]NamedQuery{Named("a", dune)}, FacetTimeZone("UTC")), "FacetTimeZone does not apply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := l.Search(ctx, "c", nil, WithOp("op", tt.op))

			var se *SearchError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v (%T), want *SearchError", err, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestOps_ErrorNamesTheOp(t *testing.T) {
	l := &Luxir{}

	_, err := l.Search(context.Background(), "c", nil,
		WithOp("outer", FieldFacet("a", SubOp("inner", RangeFacet("y", 0, 1)))))

	var oe *OpError
	if !errors.As(err, &oe) {
		t.Fatalf("err = %v (%T), want *OpError", err, err)
	}
	if oe.Op != "inner" {
		t.Errorf("OpError.Op = %q, want the innermost failing op \"inner\"", oe.Op)
	}
	if !strings.Contains(err.Error(), `"inner"`) {
		t.Errorf("err = %q, want it to name the op", err)
	}
}

func TestOps_ErrorNamesTopLevelOp(t *testing.T) {
	l := &Luxir{}

	_, err := l.Search(context.Background(), "c", nil, WithOp("decades", RangeFacet("y", 0, 1)))

	var oe *OpError
	if !errors.As(err, &oe) || oe.Op != "decades" {
		t.Errorf("err = %v, want *OpError naming \"decades\"", err)
	}
}

func TestOps_ErrorKeepsCause(t *testing.T) {
	l := &Luxir{}

	_, err := l.Search(context.Background(), "c", nil, WithOp("f", QueryFacet([]NamedQuery{Named("a", Match("t", struct{}{}))})))

	var me *MatchError
	if !errors.As(err, &me) {
		t.Errorf("err = %v (%T), want the *MatchError cause kept", err, err)
	}
}

func TestOps_SameNameInDifferentLevels(t *testing.T) {
	got := searchRequest(t, WithOp("a", FieldFacet("x_s", SubOp("a", Avg("p")))))

	want := wantRequest(t, hitsRequest(`get_number:true `+opEntry("a", `field_facet:{field:"x_s" `+opEntry("a", `expr_op:{expr:"avg(p)"}`)+`}`), ``))
	assertProto(t, got, want)
}
