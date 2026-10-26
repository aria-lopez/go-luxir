// Package luxir is a Go client for the Luxir search engine (https://luxir.org), over gRPC.
//
// Connect with NewLuxir, write with Index, IndexStream, Delete and Commit, and read with
// Search, Scan, Fuse and Do. Queries are built with helpers like Match, InRange, And and Knn,
// facets and metrics with ops like FieldFacet and Avg, and collections are managed with
// CreateCollection, SetSchema and the Schema builder.
//
//	l, err := luxir.NewLuxir("localhost:9401")
//	if err != nil {
//		return err
//	}
//	defer l.Close()
//
//	_, err = l.Index(ctx, "books", []luxir.Document{{"id": "b1", "title_t": "Dune"}}, luxir.WithCommit())
//	res, err := l.Search(ctx, "books", luxir.Match("title_t", "dune"))
//
// Luxir has no TLS or auth of its own, keep it on a private network or behind a proxy (see WithTLS).
package luxir
