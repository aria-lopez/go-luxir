# go-luxir

A Go client for the [Luxir](https://luxir.org) search engine, over gRPC.

Covers most everything I could find via the docs, the main aim of this is just to give anyone a working Luxir conn ideally without having to interface too much with the protobuf types and boilerplate

## install

```sh
go get github.com/aria-lopez/go-luxir
```

You need Go 1.26+

## quickstart

```golang
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/aria-lopez/go-luxir"
	"github.com/aria-lopez/go-luxir/luxirpb"
)

func main() {
	ctx := context.Background()
	// create new instance of luxir, if u pass "" the default it localhost:9401
	/* You can also pass this as opts for auth (luxir.NewLuxir(addr, ...opts))
	luxir.WithTLS(&tls.Config{})
	luxir.WithMaxMessageSize(32<<20)
	luxir.WithDialOptions(grpc.WithPerRPCCredentials(myToken))) // anything gRPC offers
	*/
	l, err := luxir.NewLuxir("localhost:9401")
	if err != nil {
		// returns *ConnectError here but it doesnt mean a dial failed its usually bad config with grpc.NewClient()
		log.Fatal(err)
	}
	defer l.Close()

	// once connected you can verify by grabbing your available collections
	colls, err := l.GetCollections(ctx)
	if err != nil {
		// handle *CollectionError
		log.Fatal(err)
	}
	for _, c := range colls {
		// here you get a []*luxirpb.CollectionStats
		fmt.Println(c.GetName()) // can get name or whtv u need
	}

	// set a new schema
	schema := luxir.NewSchema().
		Text("title", luxir.WithAnalyzer("unicode_word", "nfkc_cf", "fold", "kstem")). // same as _t, without an analyzer text is case sensitive
		String("author").
		Int("year", luxir.Range()). // Range() enables numeric range queries
		Date("published", luxir.Range()).
		Geo("location", luxir.Range()).
		Vector("embedding", 384, luxirpb.VectorMetric_COSINE).
		Template("_tag", luxirpb.FieldDef_STRING, luxir.Multi()).
		MustBuild() // or Build() if you dont want it to panic

	// use it in a new collection
	err = l.CreateCollection(ctx, "books", schema) // nil schema = Luxir's defaults
	if errors.Is(err, luxir.ErrCollectionExists) {
		// already there so handle it
	} else if err != nil {
		log.Fatal(err)
	}

	// get a schema
	def, err := l.GetSchema(ctx, "books")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(def.GetFields()), "fields")
	// add or update scheme
	def, err = l.SetSchema(ctx, "books", luxir.NewSchema().Double("price").MustBuild())
	// or replace it, this drops anything it doesnt list (even the default _t, _i, ... templates)
	def, err = l.ReplaceSchema(ctx, "books", schema)

	// insert some documents, the fields match the schema above (genres_tag uses the _tag template)
	_, err = l.Index(ctx, "books", []luxir.Document{
		{"id": "b1", "title": "Dune", "year": 1965, "author": "Herbert", "genres_tag": []string{"scifi", "classic"}},
		{"id": "b2", "title": "Dune Messiah", "year": 1969, "author": "Herbert", "genres_tag": []string{"scifi"}},
		{"id": "b3", "title": "Neuromancer", "year": 1984, "author": "Gibson"},
	}, luxir.WithCommit()) // withcommit means its searchable right away
	if err != nil {
		// handle *IndexError
		log.Fatal(err)
	}
	// or commit later, after writes made without WithCommit
	if _, err := l.Commit(ctx, "books"); err != nil {
		log.Fatal(err)
	}

	// get some documents and do something with them
	res, err := l.Search(ctx, "books", luxir.Match("title", "dune"), luxir.WithSort("year", false))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Found)
	for _, h := range res.Hits {
		fmt.Println(h.Score, h.Doc["id"], h.Doc["title"])
	}

	// and so much more... you get the point
}
```

## documents and values

A `luxir.Document` is a `map[string]any`. and these are the types u can use:

| Go value                                             | Luxir                              |
| ---------------------------------------------------- | ---------------------------------- |
| `string`, `bool`                                     | text / string fields               |
| `int`, `int8`-`int64`, `uint8`-`uint32`              | int fields                         |
| `float32`, `float64`                                 | float / double fields              |
| `time.Time`                                          | date fields (sent as epoch millis) |
| `luxir.Point{Lat, Lon}`, `[]luxir.Point`             | geo fields                         |
| `[]string`, `[]int`, `[]int64`, `[]float64`, `[]any` | multi-valued fields                |
| `[]float32`                                          | a vector                           |
| `[][]float32`                                        | a multi-valued vector field        |
| `nil`                                                | null                               |

`uint`, `uint64`, nested maps and structs are rejected, Luxir doesn't support them!

When reading results, ints come back as `int64`, dates as `int64` epoch millis, string lists as `[]string`, and vectors as `[]float32`. Vectors are only returned when you ask for them with `WithFields`.

## indexing

```golang
// add or replace documents: the same id replaces the whole old document
resp, err := l.Index(ctx, "books", docs, luxir.WithCommit())

// any number of documents, streamed in batches (one Index call is one gRPC message)
resp, err = l.IndexStream(ctx, "books", slices.Values(manyDocs), luxir.WithCommit())

// remove by id, an id that isn't there is just skipped
resp, err = l.Delete(ctx, "books", []string{"b1", "b2"}, luxir.WithCommit())

// make everything written so far searchable
resp, err = l.Commit(ctx, "books")
```

`IndexStream` takes an `iter.Seq[luxir.Document]`, so it can also read from a file or a channel without holding everything in memory. It sends batches over several parallel streams (roughly 3x faster than one), and every document with the same id goes down the same stream, so the last version still wins. The response merges every batch, and its error positions and returned ids follow your sequence.

Writing to a collection that doesn't exist creates it with Luxir's default schema. If you'll write to a new collection from several goroutines at once, make it first with `CreateCollection`: Luxir 0.1.0 can stall simultaneous writes into a collection that a write has only just created. `IndexStream` does this for you.

Update options (for `Index`, `IndexStream`, `Delete` and `Commit`):

| option                                                                  | what it does                                                                     |
| ----------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `WithCommit()`                                                          | commit before returning, so the change is searchable                             |
| `WithCommitWithin(d)`                                                   | commit within `d` and return without waiting (commits close together are merged) |
| `WithAllOrNone()`                                                       | one bad document rolls back the whole request (per batch for `IndexStream`)      |
| `WithReturnIDs()`                                                       | the response lists the indexed ids, in order                                     |
| `WithAllowDups()`                                                       | skip replace-by-id, faster but only when every id is new                         |
| `WithFieldMap(m)` / `WithDropUnmapped()`                                | rename document keys onto schema fields for this call                            |
| `WithWaitForMerges()`, `WithMaxSegments(n)`, `WithBuildAuxIndexes("*")` | commit-time maintenance                                                          |

Some documents can fail while others succeed. You get a `*luxir.IndexError` with the details:

```golang
_, err := l.Index(ctx, "books", docs)
var ie *luxir.IndexError
if errors.As(err, &ie) {
    fmt.Println(ie.Status()) // PARTIAL or ERROR
    for _, f := range ie.Failed() {
        fmt.Println(f.GetIndex(), f.GetId(), f.GetError().GetMessage())
    }
}
```

## collections and schemas

Default suffix templates (`_t`, `_s`, `_i`, ...) are enough to get going. For named fields, geo and vectors, build a schema:

```golang
schema := luxir.NewSchema().
    Text("title", luxir.WithAnalyzer("unicode_word", "nfkc_cf", "fold", "kstem")). // same analysis as _t
    String("author").
    Int("year", luxir.Range()). // Range() enables numeric range queries
    Date("published", luxir.Range()).
    Geo("location", luxir.Range()).
    Vector("embedding", 384, luxirpb.VectorMetric_COSINE).
    Template("_tag", luxirpb.FieldDef_STRING, luxir.Multi()).
    MustBuild() // or Build() to get every mistake as an error

err := l.CreateCollection(ctx, "books", schema) // nil schema = Luxir's defaults
if errors.Is(err, luxir.ErrCollectionExists) {
    // already there
}

def, err := l.GetSchema(ctx, "books")
def, err = l.SetSchema(ctx, "books", luxir.NewSchema().Double("price").MustBuild())  // add or redefine fields, keeps the rest
def, err = l.ReplaceSchema(ctx, "books", schema)                                     // the whole schema, drops what it leaves out

colls, err := l.GetCollections(ctx)       // stats for every collection
stats, err := l.Stats(ctx, "books", true) // one collection, with per-segment detail
err = l.DeleteCollection(ctx, "books")    // drops every document, can't be undone
```

A `Text` field with no `WithAnalyzer` isn't lowercased or folded, so it's case sensitive (`"dune"` won't find `"Dune"`); give it the analyzer above to search like the `_t` fields do. A custom schema sits on top of the default templates, so `title_t` and friends keep working. `ReplaceSchema` is the exception: it removes anything it doesn't list. Schema changes don't reindex existing documents. To change a field's type, use a new field name and reindex.

## searching

```golang
res, err := l.Search(ctx, "books", query, opts...)
```

A `nil` query matches every document. Queries:

```golang
luxir.Match("title_t", "dune messiah")                          // any term, luxir.RequireAll() for all of them
luxir.Simple(userInput, "title_t", "author_s")                  // forgiving search-box syntax, never fails to parse
luxir.Expr("title_t:$q AND year_i:>=$y", map[string]any{"q": userInput, "y": 1966}) // query language, input bound as $vars
luxir.InRange("year_i", luxir.Gte(1960), luxir.Lt(1980))        // dates take time.Time or "2024-06"
luxir.AnyOf("author_s", "Herbert", "Gibson")                    // or AnyOf("tags_ss", tagSlice)
luxir.Phrase("title_t", "dune messiah", luxir.Slop(1))
luxir.Fuzzy("title_t", "dnue", luxir.MaxEdits(1))
luxir.Prefix("title_t", "neuro")                                // also Wildcard, Regex
luxir.Exists("tags_ss")
luxir.All()
luxir.Knn("embedding", vec, 10)                                 // nearest vectors, luxir.Exact() to scan everything
luxir.GeoDistance("location", luxir.Point{Lat: 40.7, Lon: -74}, 5000)
luxir.GeoBox("location", luxir.Box{MinLat: 40, MaxLat: 41, MinLon: -75, MaxLon: -73})
```

Combine them:

```golang
luxir.And(a, b, luxir.Not(c)) // a Not inside And excludes
luxir.Or(a, b)
luxir.Bool(luxir.Must(a), luxir.Should(b, c), luxir.MinShould(1), luxir.Filter(d), luxir.MustNot(e))
luxir.Boost(a, 2)
luxir.ConstantScore(a, 1)
luxir.Rescore(a, "score * log(1 + votes_i)", nil)
```

Never paste user input into an `Expr` string. Pass it in `vars`, which Luxir binds as values and never parses, or use `Simple`, which is made for raw search-box text.

Search options:

| option                                                                         | what it does                                               |
| ------------------------------------------------------------------------------ | ---------------------------------------------------------- |
| `WithLimit(n)`                                                                 | page size, default 10, `-1` for all                        |
| `WithOffset(n)`                                                                | skip the first n, for paging                               |
| `WithSort(expr, desc)` / `WithSortVars(...)`                                   | order by a field or an expression, repeat for tie-breakers |
| `WithFilter(q)`                                                                | must also match `q`, doesn't change scores                 |
| `WithFields("id", "title_t")`                                                  | only return these fields, `*` patterns work                |
| `WithCount(false)`, `WithScores(false)`                                        | skip counting matches or scoring them                      |
| `WithTimeZone("America/Denver")`                                               | zone for dates without an offset and date math             |
| `WithFreshness(d)`                                                             | accept an index view up to `d` old                         |
| `WithOp(name, op)`                                                             | add a facet, metric, ... (see below)                       |
| `WithProfile()`, `WithRequestID(id)`, `WithBatchSize(n)`, `WithMaxParallel(n)` | tuning and debugging                                       |

An option that doesn't apply to a call, like `WithSort` on `Fuse`, returns an error. Options are never silently ignored.

### paging

```golang
for off := int64(0); ; {
    res, err := l.Search(ctx, "books", q, luxir.WithLimit(50), luxir.WithOffset(off))
    if err != nil {
        return err
    }
    use(res.Hits)
    if !res.HasMore() {
        break
    }
    off = res.NextOffset()
}
```

### exporting everything

`Scan` streams matches without holding them all in memory. Its default limit is every match.

```golang
for hit, err := range l.Scan(ctx, "books", nil, luxir.WithSort("year_i", false)) {
    if err != nil {
        return err
    }
    write(hit.Doc)
}
```

## facets and metrics

Ops run over the search's matches and come back by name in `res.Ops`:

```golang
res, err := l.Search(ctx, "books", q, luxir.WithLimit(0), // 0 = no hits, just the analytics
    luxir.WithOp("authors", luxir.FieldFacet("author_s", luxir.FacetLimit(10),
        luxir.SubOp("avg_year", luxir.Avg("year_i")),
        luxir.SubOp("newest", luxir.Docs(nil, luxir.WithLimit(1), luxir.WithSort("year_i", true))))),
    luxir.WithOp("decades", luxir.RangeFacet("year_i", 1960, 2000, luxir.Gap(10))),
    luxir.WithOp("total", luxir.Count("year_i")))

for _, b := range res.Ops.Facet("authors").Buckets {
    avg, _ := b.Ops.Float("avg_year")
    newest := b.Ops.Docs("newest").Hits
    fmt.Println(b.Value, b.Count, avg, newest)
}
total, _ := res.Ops.Int("total")
```

- **Facets:** `FieldFacet`, `RangeFacet` (with `Gap` or `CalendarGap` for dates), `QueryFacet` (named query buckets).
- **Metrics:** `Avg`, `Sum`, `Min`, `Max`, `Count`, or any aggregate expression with `Metric`.
- **Facet options:** `FacetLimit`, `MinCount`, `Missing`, `SortByMetric`, `Select`/`SelectAll`, `WithDomainQuery`, `WithDomainFilter`, `ApplyParentFilters`.
- `InDomain(op, ...)` runs any op over a different set of documents.

Multi-select navigation, where the selected facet still shows its other values:

```golang
res, err := l.Search(ctx, "books", nil,
    luxir.WithFilterExcept(luxir.Match("author_s", "Gibson"), "authors"),
    luxir.WithOp("authors", luxir.FieldFacet("author_s")))
```

## hybrid search

`Fuse` merges ranked lists with reciprocal rank fusion. This is the usual way to combine text and vector search:

```golang
res, err := l.Fuse(ctx, "books", []luxir.Source{
    luxir.FuseSource("text", luxir.Match("title_t", userInput), luxir.WithLimit(50)),
    luxir.FuseSource("vector", luxir.Knn("embedding", vec, 50)),
}, luxir.WithLimit(10))
```

`Do` sends several independent ops in one request, each over the whole collection unless you give it a domain:

```golang
out, err := l.Do(ctx, "books", map[string]luxir.Op{
    "authors": luxir.FieldFacet("author_s"),
    "avg":     luxir.Avg("year_i"),
    "recent":  luxir.Docs(luxir.InRange("year_i", luxir.Gte(1980)), luxir.WithLimit(5)),
})
recent := out.Ops.Docs("recent")
```

## errors

Every call returns a typed error. Use `errors.As` to inspect it:

| error                                                       | from                                                                                               |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `*luxir.SearchError`                                        | `Search`, `Scan`, `Fuse`, `Do`. `RequestFailed()` is Luxir's own rejection (kind, code, message)   |
| `*luxir.IndexError`                                         | `Index`, `IndexStream`, `Commit`. Per-document failures and the request-level error                |
| `*luxir.DeleteError`                                        | `Delete`                                                                                           |
| `*luxir.CollectionError`                                    | collection, schema, stats and cache calls. Matches `ErrCollectionExists` / `ErrCollectionNotFound` |
| `*luxir.QueryError`, `*luxir.OpError`, `*luxir.SchemaError` | a helper got bad input. Caught before anything is sent                                             |
| `*luxir.ConnectError`, `*luxir.HealthCheckError`            | `NewLuxir`, `HealthCheck`                                                                          |

The gRPC status is kept underneath, so `status.Code(err)` still works (for example, `codes.Unavailable` when the node is down).

## connecting and security

Luxir has no TLS, authentication or authorization. It expects to sit on a private network, behind a proxy or service mesh. Don't expose it directly. The client connects in plaintext by default. For a TLS proxy in front of Luxir:

```golang
l, err := luxir.NewLuxir("search.internal:443",
    luxir.WithTLS(&tls.Config{}),           // system roots, or set RootCAs
    luxir.WithMaxMessageSize(32<<20),       // accept responses over gRPC's 4 MiB default
    luxir.WithDialOptions(grpc.WithPerRPCCredentials(myToken))) // anything else gRPC offers
```

`l.HealthCheck(ctx)` asks the node whether it's serving. A `*Luxir` is safe to share between goroutines. Make one and reuse it.

## escape hatches

Everything in Luxir's API has a helper. For something new or unusual, `luxir.Raw` takes a protobuf query directly, and the `luxirpb` package has every request and response type:

```golang
q := &luxirpb.Query{Kind: &luxirpb.Query_Phrase{Phrase: &luxirpb.PhraseQuery{Field: "title_t", Text: "dune messiah"}}}
res, err := l.Search(ctx, "books", luxir.Raw(q))

resp, err := l.CacheControl(ctx, &luxirpb.CacheControlRequest{Collection: "books", Flush: true})
```

## performance

Measured against a local Luxir 0.1.0 node (RAM storage), with the client and the server on the same AMD Ryzen 5 1600X (6 cores / 12 threads). There were 100k documents of 6 fields each, and 16 workers for throughput. "Raw gRPC" is hand-written code on the generated stubs, and "HTTP" is Luxir's JSON API through Go's `net/http`. Your numbers will differ. The point is the comparison.

| | go-luxir | raw gRPC | HTTP |
|---|---|---|---|
| bulk ingest | **~330k docs/s** (`IndexStream`) | ~150k docs/s (one `UpdateStream`) | ~225k docs/s (NDJSON stream) |
| ingest, 1000-doc calls | ~88k docs/s (`Index`) | ~82k docs/s | ~120k docs/s |
| top-10 search, p50 | ~460 µs | ~400 µs | ~290 µs |
| search throughput | ~10k/s | ~10k/s | ~22k/s |
| facet search, p50 | ~340 µs | ~300 µs | ~240 µs |
| export everything | ~135k docs/s (`Scan`) | ~150k docs/s | ~178k docs/s (NDJSON) |

`IndexStream` is the fastest way to load data, because it spreads batches over parallel streams that Luxir indexes concurrently. For searches, the client adds a few tens of microseconds over hand-written gRPC, almost all of it turning results into Go values. Luxir currently answers small searches faster over HTTP than over gRPC, whatever the client.

## tests

```sh
go test ./...                              # live tests skip if no node is running
LUXIR_ADDR=host:9401 go test -race ./...   # point at another node
```

Most tests run against a real Luxir node, in throwaway collections that are deleted afterwards. The rest use an in-process fake server.

## feel free to contribute

If you want to contribute feel free to open a PR. Make sure the code follows a similar style to the existing code and ideally make sure to have good tests for proposed changes.

**AI USAGE**

I don't care if you use AI to write your code, but absolutely do not try to commit code that you didn't read line by line and or do not understand.

## license

MIT, see [LICENSE](LICENSE).
