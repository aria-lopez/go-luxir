# go-luxir

Luxir client written in go via gRPC

## api

This is in active development, however will be at a MVP state soon.

Currently supports:

- NewLuxir() Establish Luxir client at ur addr
- Index() add docs to a collection
- Search() Search a collection; tons of helpers too!
- luxir.Raw() takes a protobuf query so you can technically do any query already, but you get no control flow. This is also rather annoying to work with because you have to use &luxirpb to manually format everything yourself.

See examples of luxir.Raw() below, I will add docs for the helpers soon too.

What I will work on next:

- Delete
- Collections (manage/create/etc)
- Connection options (TLS etc)
- More item options, currently we only support WithCommit()
- More Query Helpers (Range, And/Or/Not, Phrase, Expr)
  - Please note: luxir.Raw() builds any query currently but provides no error control flow
- Batch calls (for large documents) (gRPC limits to 4MB I believe)
- Docs: Ill write up a robust doc and add some examples once all the above are implemented and then it will be in a MVP state.

What I may add after:

- HTTP support. HTTP and gRPC route through to the same place at the end of the day, but a HTTP only lib could be written only using standard and avoid dealing with gRPC.

## feel free to contribute

If you want to contribute feel free to open a PR. Make sure the code follows a similar style to the existing code and ideally make sure to have good tests for proposed changes.

**AI USAGE**

I don't care if you use AI to write your code, but absolutely do not try to commit code that you didn't read line by line and or do not understand.

## Examples

### using luxir.Raw() for unimpl helpers (Range, Boolean, Expr, Expr+Bound, Phrase)

These will all be rolled in to helpers soon, but Raw will be forward compatible if the Luxir API ever changes.

As you can see its a lot of boilerplate so try to use the built in helpers when possible.

```golang
import "github.com/aria-lopez/go-luxir/luxirpb"

i := func(n int64) *luxirpb.Val { return &luxirpb.Val{Kind: &luxirpb.Val_I{I: n}} }
s := func(x string) *luxirpb.Val { return &luxirpb.Val{Kind: &luxirpb.Val_S{S: x}} }

// Range example (1960->1979)
q := &luxirpb.Query{Kind: &luxirpb.Query_Range{Range: &luxirpb.RangeQuery{
    Field: "year_i", Gte: i(1960), Lt: i(1980),
}}}
res, err := l.Search(ctx, "books", luxir.Raw(q), luxir.WithSort("year_i", false))

// Boolean example (matches "dune" but not 1969)
q := &luxirpb.Query{Kind: &luxirpb.Query_Boolean{Boolean: &luxirpb.BooleanQuery{
    Required: []*luxirpb.Query{
        {Kind: &luxirpb.Query_Match{Match: &luxirpb.Match{Field: "title_t", Val: s("dune")}}},
    },
    Prohibited: []*luxirpb.Query{
        {Kind: &luxirpb.Query_Range{Range: &luxirpb.RangeQuery{Field: "year_i", Gte: i(1969), Lte: i(1969)}}},
    },
}}}
res, err := l.Search(ctx, "books", luxir.Raw(q))

// Expr language example, this is the simplest
q := &luxirpb.Query{Kind: &luxirpb.Query_Expr{Expr: &luxirpb.ExprQuery{
    Q: "title_t:dune AND year_i:[1960 TO 1980] AND NOT year_i:1969",
}}}
res, err := l.Search(ctx, "books", luxir.Raw(q))

// Expr w/ bound variables.
q := &luxirpb.Query{Kind: &luxirpb.Query_Expr{Expr: &luxirpb.ExprQuery{
    Q:    "title_t:$title AND year_i:>=$minYear",
    Vars: map[string]*luxirpb.Val{"title": s(userInput), "minYear": i(1966)},
}}}
res, err := l.Search(ctx, "books", luxir.Raw(q))

// Phrase query
q := &luxirpb.Query{Kind: &luxirpb.Query_Phrase{Phrase: &luxirpb.PhraseQuery{
    Field: "title_t", Text: "dune messiah",
}}}

```
