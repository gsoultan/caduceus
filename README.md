# caduceus

A PostgreSQL driver for Go, written from the protocol up. Pure Go, standard
library only: no `database/sql`, no pgx, no pglogrepl. `go.mod` requires nothing.

```go
import "github.com/gsoultan/caduceus"

conn, err := caduceus.Connect(ctx, "postgres://app:secret@db:5432/app?sslmode=require", caduceus.Options{})
if err != nil {
	return err
}
defer conn.Close(ctx)

var name string
err = conn.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", 42).Scan(&name)
```

## What it does

| | |
| :--- | :--- |
| **Connect** | URL or key=value strings, `PG*` environment variables, the password file, TLS by `sslmode` (`disable` … `verify-full`), SCRAM-SHA-256, MD5 and cleartext auth, Unix sockets |
| **Query** | `Exec`, `Query`, `QueryRow`, `QueryInto`; parameters always bound server-side, never interpolated |
| **Transactions** | `Begin`, `BeginTx` with isolation level, read-only and deferrable; nested transactions as savepoints; idempotent `Commit` / `Rollback` |
| **Batch** | `SendBatch`: any number of queries in at most two round trips |
| **COPY** | `CopyFrom` streams rows in 64 KiB chunks |
| **LISTEN/NOTIFY** | `WaitForNotification`, which never aborts a connection on timeout |
| **Cancellation** | a context ending mid-query sends a CancelRequest and closes the connection |
| **Statement cache** | LRU of server-side prepared statements, 64 by default; disable it for a pooler in transaction mode |
| **Types** | integers, floats, numeric, bool, text, bytea, uuid, date/time/timestamp/timestamptz/interval, json/jsonb, arrays; `codec.Scanner` and `codec.Valuer` for your own types; a pointer destination receives NULL |
| **Errors** | `*wire.Error` with the SQLSTATE and every field the server sends; classify by `SQLState()`, never by message text |
| **Logical replication** | package `replication`: pgoutput v1 decoder, XLogData, keepalives, standby status, LSNs |

### Transactions

```go
tx, err := conn.Begin(ctx)
if err != nil {
	return err
}
defer tx.Rollback(ctx) // returns ErrTxClosed after Commit, and sends nothing

if _, err := tx.Exec(ctx, "UPDATE accounts SET balance = balance - $1 WHERE id = $2", 100, 1); err != nil {
	return err
}

// A nested transaction is a savepoint: rolling it back undoes only its own work,
// and the outer transaction stays usable even after an error inside it.
sp, err := tx.Begin(ctx)
if err != nil {
	return err
}
if _, err := sp.Exec(ctx, "INSERT INTO audit VALUES ($1)", "debit"); err != nil {
	_ = sp.Rollback(ctx)
} else if err := sp.Commit(ctx); err != nil {
	return err
}

return tx.Commit(ctx) // ErrTxCommitRollback if the server rolled it back instead
```

`BeginTx(ctx, caduceus.TxOptions{IsoLevel: caduceus.Serializable, ReadOnly: true})`
sets the characteristics. Only the declared isolation levels are accepted, since
the level is written into `BEGIN` rather than bound.

### Batch and COPY

```go
results, err := conn.SendBatch(ctx, []caduceus.Query{
	{SQL: "SELECT balance FROM accounts WHERE id = $1", Arguments: []any{1}},
	{SQL: "SELECT balance FROM accounts WHERE id = $1", Arguments: []any{2}},
})
if err != nil {
	return err
}
defer results.Close()
var a, b int64
if err := results.QueryRow().Scan(&a); err != nil {
	return err
}
if err := results.QueryRow().Scan(&b); err != nil {
	return err
}

n, err := conn.CopyFrom(ctx, []string{"public", "events"}, []string{"id", "kind"},
	caduceus.CopyFromRows([][]any{{1, "signup"}, {2, "login"}}))
```

## Concurrency

A `Conn` is one connection and belongs to one goroutine at a time; it has no
lock, because the protocol is a strict request/response sequence. A result not
read to its end keeps the connection busy, and the next call returns
`ErrConnBusy` rather than interleaving with it. For many goroutines, pool
connections with [gpool](https://github.com/gsoultan/gpool), which is built on
caduceus.

## Performance

One connection, one caller, so each number is the driver's own cost per
operation with no pool or queueing in it. `database/sql` runs over pgx's stdlib
driver with its pool held to one connection. These are medians of three 3-second
runs on PostgreSQL 16.15 over loopback TCP, on a 4-vCPU 2.3 GHz Xeon, Go 1.26.5.

| Workload | caduceus | pgx v5.10 | database/sql |
| :--- | ---: | ---: | ---: |
| QueryRow, one int8 parameter | **39.2 µs** · 1 alloc | 50.4 µs · 5 allocs | 55.0 µs · 15 allocs |
| 100 rows × 4 typed columns | **117 µs** · 105 allocs | 171 µs · 662 allocs | 181 µs · 525 allocs |
| BEGIN, INSERT, COMMIT | **149 µs** · 5 allocs | 157 µs · 6 allocs | 175 µs · 16 allocs |
| Batch of 10 selects | **61.0 µs** · 12 allocs | 80.9 µs · 161 allocs | 490 µs · 140 allocs ¹ |
| COPY 1,000 rows | **1.41 ms** · 0.5 KiB | 1.60 ms · 117 KiB | 67.5 ms ² |
| Decode a pgoutput UPDATE (vs pglogrepl) | **855 ns** · 20 allocs | 1,469 ns · 48 allocs | — |

¹ database/sql has no batch, so the ten queries run one after another.
² database/sql has no COPY, so the rows are inserted with a prepared statement in one transaction.

Reproduce with:

```bash
cd benchmarks
DATABASE_URL='postgres://…' go test -run='^$' -bench=. -benchtime=3s -count=3 ./
```

The benchmarks are a module of their own, so pgx and pglogrepl never reach
an application that imports caduceus.

## Packages

| Package | Contents |
| :--- | :--- |
| `caduceus` | `Conn`, `Rows`, `Row`, `Tx`, `BatchResults`, `CopyFrom` |
| `caduceus/wire` | the frontend/backend protocol, version 3.0: config, TLS, auth, framing, errors, cancel, notifications, CopyBoth |
| `caduceus/codec` | Go values to and from PostgreSQL's text and binary formats |
| `caduceus/replication` | logical replication: pgoutput decoding, stream messages, LSNs |

## Testing

```bash
go test -race ./...                                   # no database needed
DATABASE_URL='postgres://…' go test -race ./...       # adds the live tests
```

## License

MIT
