package luxir

import (
	"context"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// CreateCollection ;; make new collection
//
// ;; schema nil = luxir's default
// ;; returns *CollectionError or ErrCollectionExists if the name is exists alr
func (l *Luxir) CreateCollection(ctx context.Context, name string, schema *luxirpb.SchemaDef) error {
	if name == "" {
		return &CollectionError{Op: "create", Msg: "name is required"}
	}
	_, err := l.admin.CreateCollection(ctx, &luxirpb.CreateCollectionRequest{Name: name, Schema: schema})
	if err != nil {
		return &CollectionError{Op: "create", Name: name, Err: err}
	}
	return nil
}

// DeleteCollection ;; drop a collection and all its docs, this is destructive!
//
// ;; name is required for obv reason we dont default to "main"
func (l *Luxir) DeleteCollection(ctx context.Context, name string) error {
	if name == "" {
		return &CollectionError{Op: "delete", Msg: "name is required"}
	}
	_, err := l.admin.DeleteCollection(ctx, &luxirpb.DeleteCollectionRequest{Name: name})
	if err != nil {
		return &CollectionError{Op: "delete", Name: name, Err: err}
	}
	return nil
}

// GetCollections ;; get a *luxirpb.CollectionStats for each coll on the node
func (l *Luxir) GetCollections(ctx context.Context) ([]*luxirpb.CollectionStats, error) {
	resp, err := l.admin.Stats(ctx, &luxirpb.StatsRequest{})
	if err != nil {
		return nil, &CollectionError{Op: "list", Err: err}
	}
	return resp.GetCollections(), nil
}

// GetSchema ;; the collection's schema as it was written (fields and templates)
//
// ;; collection "" defaults to "main"
// ;; returns *CollectionError, ErrCollectionNotFound if there's no such collection
func (l *Luxir) GetSchema(ctx context.Context, collection string) (*luxirpb.SchemaDef, error) {
	resp, err := l.admin.GetSchema(ctx, &luxirpb.SchemaRequest{Collection: collection})
	if err != nil {
		return nil, &CollectionError{Op: "get schema", Name: collection, Err: err}
	}
	return resp.GetSchema(), nil
}

// SetSchema ;; set each field and template in schema whole, everything it doesn't name is kept
//
// ;; a collection that doesn't exist is made, existing documents are not reindexed
// ;; change a field's type with a new field
// ;; returns the resulting schema, or *CollectionError
func (l *Luxir) SetSchema(ctx context.Context, collection string, schema *luxirpb.SchemaDef) (*luxirpb.SchemaDef, error) {
	return l.schema(ctx, "set schema", collection, schema, luxirpb.SchemaRequest_SET)
}

// ReplaceSchema ;; replace the whole schema, fields and templates it leaves out are removed
//
// ;; returns the resulting schema, or *CollectionError
func (l *Luxir) ReplaceSchema(ctx context.Context, collection string, schema *luxirpb.SchemaDef) (*luxirpb.SchemaDef, error) {
	return l.schema(ctx, "replace schema", collection, schema, luxirpb.SchemaRequest_REPLACE_ALL)
}

func (l *Luxir) schema(ctx context.Context, op, collection string, schema *luxirpb.SchemaDef, mode luxirpb.SchemaRequest_Mode) (*luxirpb.SchemaDef, error) {
	if schema == nil {
		return nil, &CollectionError{Op: op, Msg: "schema is required"}
	}
	resp, err := l.admin.SetSchema(ctx, &luxirpb.SchemaRequest{Collection: collection, Mode: mode, Schema: schema})
	if err != nil {
		return nil, &CollectionError{Op: op, Name: collection, Err: err}
	}
	return resp.GetSchema(), nil
}

// Stats ;; document counts, segments, sizes and cache counters
//
// ;; collection "" is every collection on the node, segments adds per-segment detail
// ;; an absent count is zero
func (l *Luxir) Stats(ctx context.Context, collection string, segments bool) (*luxirpb.StatsResponse, error) {
	resp, err := l.admin.Stats(ctx, &luxirpb.StatsRequest{Collection: collection, Segments: segments})
	if err != nil {
		return nil, &CollectionError{Op: "stats", Name: collection, Err: err}
	}
	return resp, nil
}

// CacheControl ;; flush, reset or dump the query cache, an empty collection is every collection
func (l *Luxir) CacheControl(ctx context.Context, req *luxirpb.CacheControlRequest) (*luxirpb.CacheControlResponse, error) {
	if req == nil {
		req = &luxirpb.CacheControlRequest{}
	}
	resp, err := l.admin.CacheControl(ctx, req)
	if err != nil {
		return nil, &CollectionError{Op: "cache control", Name: req.GetCollection(), Err: err}
	}
	return resp, nil
}
