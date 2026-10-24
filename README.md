# go-luxir

Luxir client written in go via gRPC

## api

This is in active development, however will be at a MVP state soon.

Currently supports:

- NewLuxir() Establish Luxir client at ur addr
- Index() add docs to a collection
- Search() Search a collection; tons of helpers too!

What I will work on next:

- Delete
- Collections (manage/create/etc)
- Connection options (TLS etc)
- More item options, currently we only support WithCommit()
- More Query Helpers (Range, And/Or/Not, Phrase, Expr)
  - Please note: luxir.Raw() builds any query currently but provides no error control flow
- Batch calls (for large documents) (gRPC limits to 4MB I believe)
- Docs: Ill write up a robust doc and add some examples once all the above are implemented and then it will be in a MVP state.

## feel free to contribute

If you want to contribute feel free to open a PR. Make sure the code follows a similar style to the existing code and ideally make sure to have good tests for proposed changes.

**AI USAGE**

I don't care if you use AI to write your code, but absolutely do not try to commit code that you didn't read line by line and or do not understand.
