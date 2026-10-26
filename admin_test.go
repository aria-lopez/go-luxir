package luxir

import (
	"context"
	"errors"
	"testing"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

func TestSchema_GetSetReplace(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	if err := l.CreateCollection(timeout(t), coll, NewSchema().Text("title").MustBuild()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got, err := l.GetSchema(timeout(t), coll)
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if got.GetFields()["title"].GetType() != luxirpb.FieldDef_TEXT || got.GetTemplates()["_t"] == nil {
		t.Errorf("schema = %v, want title plus the default templates", got)
	}

	set, err := l.SetSchema(timeout(t), coll, NewSchema().Int("year", Range()).MustBuild())
	if err != nil {
		t.Fatalf("SetSchema: %v", err)
	}
	if set.GetFields()["title"] == nil || set.GetFields()["year"].GetIndex() != luxirpb.FieldDef_RANGE {
		t.Errorf("after set = %v, want title kept and year added", set.GetFields())
	}
	if _, err := l.Index(timeout(t), coll, []Document{{"id": "a", "title": "Dune", "year": 1965}}, WithCommit()); err != nil {
		t.Fatalf("Index into the new fields: %v", err)
	}
	r, err := l.Search(timeout(t), coll, InRange("year", Gte(1960)))
	if err != nil || r.Found != 1 {
		t.Errorf("range on the new field = %v, %v", r, err)
	}

	replaced, err := l.ReplaceSchema(timeout(t), coll, NewSchema().String("code").MustBuild())
	if err != nil {
		t.Fatalf("ReplaceSchema: %v", err)
	}
	if replaced.GetFields()["title"] != nil || replaced.GetFields()["code"] == nil || len(replaced.GetTemplates()) != 0 {
		t.Errorf("after replace = %v, want only code (and the reserved fields)", replaced)
	}
	again, err := l.GetSchema(timeout(t), coll)
	if err != nil || again.GetFields()["code"] == nil {
		t.Errorf("GetSchema after replace = %v, %v", again, err)
	}
}

func TestSchema_SetMakesTheCollection(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)

	if _, err := l.SetSchema(timeout(t), coll, NewSchema().Text("x").MustBuild()); err != nil {
		t.Fatalf("SetSchema: %v", err)
	}
	got, err := l.GetSchema(timeout(t), coll)
	if err != nil || got.GetFields()["x"] == nil {
		t.Errorf("GetSchema = %v, %v, want the collection made with x", got, err)
	}
}

func TestSchema_NotFound(t *testing.T) {
	l := getClient(t)

	_, err := l.GetSchema(timeout(t), newCollection(t, l))

	var ce *CollectionError
	if !errors.As(err, &ce) || ce.Op != "get schema" || !errors.Is(err, ErrCollectionNotFound) {
		t.Errorf("err = %v, want *CollectionError matching ErrCollectionNotFound", err)
	}
}

func TestSchema_Rejected(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	if err := l.CreateCollection(timeout(t), coll, nil); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	bad := &luxirpb.SchemaDef{Fields: map[string]*luxirpb.FieldDef{"x": {Parent: "_nope"}}}

	_, err := l.SetSchema(timeout(t), coll, bad)

	var ce *CollectionError
	if !errors.As(err, &ce) || ce.Err == nil {
		t.Errorf("err = %v, want *CollectionError with Luxir's rejection", err)
	}
}

func TestSchema_NilSchema(t *testing.T) {
	l := &Luxir{}
	for name, call := range map[string]func() error{
		"set":     func() error { _, err := l.SetSchema(context.Background(), "c", nil); return err },
		"replace": func() error { _, err := l.ReplaceSchema(context.Background(), "c", nil); return err },
	} {
		var ce *CollectionError
		if err := call(); !errors.As(err, &ce) || ce.Msg != "schema is required" {
			t.Errorf("%s: err = %v, want the nil schema caught before the network", name, err)
		}
	}
}

func TestStats(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	if _, err := l.Index(timeout(t), coll, numbered(3), WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	one, err := l.Stats(timeout(t), coll, false)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(one.GetCollections()) != 1 || one.GetCollections()[0].GetName() != coll || one.GetCollections()[0].GetTotals().GetLiveDocs() != 3 {
		t.Errorf("stats = %v, want just %s with 3 live docs", one.GetCollections(), coll)
	}
	if segs := one.GetCollections()[0].GetShards()[0].GetIndex().GetSegments(); len(segs) != 0 {
		t.Errorf("segments = %d, want none unless asked", len(segs))
	}

	withSegs, err := l.Stats(timeout(t), coll, true)
	if err != nil {
		t.Fatalf("Stats segments: %v", err)
	}
	if segs := withSegs.GetCollections()[0].GetShards()[0].GetIndex().GetSegments(); len(segs) == 0 {
		t.Error("segments = none, want per-segment detail")
	}

	all, err := l.Stats(timeout(t), "", false)
	if err != nil {
		t.Fatalf("Stats all: %v", err)
	}
	if all.GetTotals().GetCollections() < 1 || len(all.GetCollections()) < 1 {
		t.Errorf("node stats = %v, want every collection", all.GetTotals())
	}
}

func TestStats_NotFound(t *testing.T) {
	l := getClient(t)

	_, err := l.Stats(timeout(t), newCollection(t, l), false)

	if !errors.Is(err, ErrCollectionNotFound) {
		t.Errorf("err = %v, want ErrCollectionNotFound", err)
	}
}

func TestCacheControl(t *testing.T) {
	l := getClient(t)
	coll := newCollection(t, l)
	if _, err := l.Index(timeout(t), coll, numbered(3), WithCommit()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	resp, err := l.CacheControl(timeout(t), &luxirpb.CacheControlRequest{Collection: coll, Flush: true, ResetCounters: true, Dump: true})
	if err != nil {
		t.Fatalf("CacheControl: %v", err)
	}
	if len(resp.GetCollections()) != 1 || resp.GetCollections()[0].GetName() != coll {
		t.Errorf("collections = %v, want just %s", resp.GetCollections(), coll)
	}

	if _, err := l.CacheControl(timeout(t), nil); err != nil {
		t.Errorf("CacheControl(nil) = %v, want every collection with no actions", err)
	}
}

func TestCacheControl_NotFound(t *testing.T) {
	l := getClient(t)

	_, err := l.CacheControl(timeout(t), &luxirpb.CacheControlRequest{Collection: newCollection(t, l)})

	if !errors.Is(err, ErrCollectionNotFound) {
		t.Errorf("err = %v, want ErrCollectionNotFound", err)
	}
}
