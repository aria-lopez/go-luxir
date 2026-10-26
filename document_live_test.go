package luxir

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func TestDocumentLive_PointsAndMultiVectors(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	def := NewSchema().
		Geo("loc", Range()).
		Geo("stops", Range(), Multi()).
		Vector("mv", 2, luxirpb.VectorMetric_L2, Multi()).
		MustBuild()
	if err := l.CreateCollection(timeout(t), coll, def); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	nyc, london := Point{Lat: 40.71, Lon: -74.0}, Point{Lat: 51.51, Lon: -0.13}

	_, err := l.Index(timeout(t), coll, []Document{
		{"id": "a", "loc": nyc, "stops": []Point{nyc, london}, "mv": [][]float32{{1, 0}, {0, 1}}},
		{"id": "b", "loc": london, "stops": [][]float64{{-0.13, 51.51}}, "mv": [][]float32{{5, 5}}},
	}, WithCommit())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"point near nyc", GeoDistance("loc", nyc, 1000), []string{"a"}},
		{"point near london", GeoDistance("loc", london, 1000), []string{"b"}},
		{"any stop near london", GeoDistance("stops", london, 1000), []string{"a", "b"}},
		{"nearest of many vectors", Knn("mv", []float32{0, 1}, 1, Exact()), []string{"a"}},
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

	r, err := l.Search(timeout(t), coll, Match("id", "a"), WithFields("id", "mv"))
	if err != nil {
		t.Fatalf("Search with a multi-vector field: %v", err)
	}
	if got := r.Hits[0].Doc["mv"]; !reflect.DeepEqual(got, [][]float32{{1, 0}, {0, 1}}) {
		t.Errorf("mv = %#v, want [][]float32 back", got)
	}
}

func TestQuery_ConcurrentReuse(t *testing.T) {
	l, _ := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))
	queries := []Query{
		Phrase("t", "a b", Slop(1)), PhraseWords("t", []string{"a"}, Slop(2)), Fuzzy("t", "x", MaxEdits(1)),
		Knn("v", []float32{1}, 1, Nprobe(2)), Match("t", "x", RequireAll()), Bool(Must(dune), Should(messiah)),
	}
	op := FieldFacet("f", SubOp("avg", Avg("p")), Select("a"))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		for _, q := range queries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := l.Search(timeout(t), "c", q, WithOp("f", op)); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
}

func TestOptions_NilIsSkipped(t *testing.T) {
	l, f := fakeLuxir(t, fakeReply(&luxirpb.SearchResponse{}))

	_, err := l.Search(timeout(t), "c", Match("t", "x", nil), nil, WithLimit(3), WithOp("f", FieldFacet("f", nil)))
	if err != nil {
		t.Fatalf("Search with nil options: %v", err)
	}
	if f.last(t).GetOps()[opName].GetTopDocs().GetLimit() != 3 {
		t.Error("the non-nil options were lost")
	}

	idx := fakeIndexerLuxir(t, func(s updateStream) error {
		for {
			r, err := s.Recv()
			if err != nil {
				return nil
			}
			s.Send(&luxirpb.UpdateResponse{RequestId: r.GetRequestId(), Status: luxirpb.UpdateResponse_OK})
		}
	})
	if _, err := idx.IndexStream(context.Background(), "c", nil, nil, WithCommit(), nil); err != nil {
		t.Errorf("IndexStream with nil options: %v", err)
	}
	if _, err := NewSchema().Text("a", nil).Build(); err != nil {
		t.Errorf("schema with a nil option: %v", err)
	}
}
