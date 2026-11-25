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
| **Batch** | `SendBatch`: any number of queries as one transaction, in at most two round trips when they fit a 32 KiB window and a window at a time beyond that |
| **Pipeline** | `SendPipeline`: any number of statements sent without waiting for each reply, each failing on its own; inside a transaction, the fastest way to run many statements |
| **COPY** | `CopyFrom` streams rows in 64 KiB chunks, in binary where the column types allow it |
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

`CopyFrom` takes two round trips whatever the number of rows. Outside a
transaction it runs in one of its own, so the copy is all or nothing and the
column types it was encoded for cannot change under it; inside one it is part
of yours. The rows go in
COPY's binary format, which the server reads without parsing, when every column
is an integer, float, bool, text, varchar, char, bytea, uuid, date, timestamp,
timestamptz, interval, json or jsonb, and in text when any column is something
else. A value only the server can read, such as the string `"42"` for an integer
column, is not refused: from that row on the rows go in text, as a second COPY
that commits or fails together with the first.

### Pipeline

A batch is one transaction: the first statement that fails ends it. A pipeline
sends its statements the same way, without waiting for each reply, but every
statement stands alone, so one that fails reports its error and the rest still
run.

```go
results, err := conn.SendPipeline(ctx, []caduceus.Query{
	{SQL: "INSERT INTO events (id, kind) VALUES ($1, $2)", Arguments: []any{1, "signup"}},
	{SQL: "INSERT INTO events (id, kind) VALUES ($1, $2)", Arguments: []any{1, "duplicate"}},
	{SQL: "INSERT INTO events (id, kind) VALUES ($1, $2)", Arguments: []any{2, "login"}},
})
if err != nil {
	return err
}
defer results.Close()
for range 3 {
	if _, err := results.Exec(); err != nil {
		log.Print(err) // the second insert; the third still ran
	}
}
```

Statements are sent a 32 KiB window at a time as their results are read, so a
pipeline of a million statements holds two windows of encoded messages, not a
million. `Close` runs and reads whatever was not read.

A batch larger than one window is sent the same way. Neither ever has more
unread by the server than a server's initial receive window, so the client is
never left writing to a server that has stopped reading because the client has
not read its replies, which is how a driver that sends everything first can
stall on a large batch.

Inside a transaction use `tx.SendPipeline`. There is nothing to isolate there,
since the first failure aborts the transaction whatever follows, so the server
is asked to answer a whole window at once instead of statement by statement.
That is the fastest way to run many different statements: a million inserts in
one transaction take a fifth of the client CPU of a pgx batch.

## Concurrency

A `Conn` is one connection and belongs to one goroutine at a time; it has no
lock, because the protocol is a strict request/response sequence. A result not
read to its end keeps the connection busy, and the next call returns
`ErrConnBusy` rather than interleaving with it. For many goroutines, pool
connections with [gpool](https://github.com/gsoultan/gpool), which is built on
caduceus.

## Performance

One connection, one caller, so each number is the driver's own cost per
operation with no pool or queueing in it. `database/sql` is measured twice, over
pgx's stdlib driver and over lib/pq, each with its pool held to one connection.
These are medians of three 3-second runs on PostgreSQL 16.15 over loopback TCP,
on a 4-vCPU 2.1 GHz Xeon, Go 1.26.5.

Wall time per operation:

| Workload | caduceus | pgx v5.10 | database/sql + pgx | database/sql + lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| QueryRow, one int8 parameter | **47.5 µs** | 64.2 µs | 69.8 µs | 112 µs |
| 100 rows × 4 typed columns | **146 µs** | 229 µs | 217 µs | 307 µs |
| BEGIN, INSERT, COMMIT | **147 µs** | 211 µs | 222 µs | 274 µs |
| Batch of 10 selects | **76.3 µs** | 107 µs | 693 µs ¹ | 1.13 ms ¹ |
| COPY 1,000 rows | **1.84 ms** | 2.13 ms | — ² | 2.54 ms ³ |
| 1,000 inserts in one transaction | **3.09 ms** ⁴ | 4.76 ms ⁴ | 68 ms | 54 ms |
| Decode a pgoutput UPDATE | **1.06 µs** | 1.86 µs (pglogrepl) | — | — |

Wall time includes the server's share of every round trip, which no driver can
change. Client CPU per operation is the driver's own (`cpu-ns/op` in the
benchmark output, the process's user and system time):

| Workload | caduceus | pgx v5.10 | database/sql + pgx | database/sql + lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| QueryRow | **30.4 µs** | 53.7 µs | 60.0 µs | 69.4 µs |
| 100 rows × 4 typed columns | **64.6 µs** | 152 µs | 138 µs | 186 µs |
| BEGIN, INSERT, COMMIT | **90.4 µs** | 165 µs | 197 µs | 209 µs |
| Batch of 10 selects | **41.6 µs** | 84.5 µs | 591 µs | 700 µs |
| COPY 1,000 rows | **236 µs** | 622 µs | — | 1.01 ms |
| 1,000 inserts in one transaction | **588 µs** | 2.63 ms | 55 ms | 34 ms |

Memory allocated per operation:

| Workload | caduceus | pgx v5.10 | database/sql + pgx | database/sql + lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| QueryRow | **263 B** · 1 alloc | 351 B · 5 | 759 B · 15 | 703 B · 17 |
| 100 rows × 4 typed columns | **1.9 KiB** · 105 | 11 KiB · 662 | 8.2 KiB · 525 | 12 KiB · 623 |
| BEGIN, INSERT, COMMIT | **95 B** · 5 | 192 B · 6 | 725 B · 16 | 767 B · 17 |
| Batch of 10 selects | **2.7 KiB** · 12 | 6.7 KiB · 161 | 7.3 KiB · 140 | 6.7 KiB · 160 |
| COPY 1,000 rows | **0.7 KiB** · 22 | 114 KiB · 43 | — | 222 KiB · 4,027 |
| 1,000 inserts in one transaction | **33 KiB** · 12 | 809 KiB · 9,037 | 252 KiB · 6,033 | 204 KiB · 7,014 |

¹ database/sql has no batch, so the ten queries run one after another.
² pgx's database/sql driver has no COPY.
³ lib/pq's `CopyIn`, which needs a transaction around it.
⁴ `tx.SendPipeline` for caduceus, `SendBatch` for pgx.

### A million rows

Each cell is wall time · client CPU · memory allocated, for one pass over a
million rows: reading four typed columns, copying two columns in, and inserting
two columns with a statement per row in one transaction. Medians of three to
seven passes on the same machine.

| Workload | caduceus | pgx v5.10 | database/sql + pgx | lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| Read 1M rows | 594 ms · **292 ms** · **15 MiB** | 679 ms · 641 ms · 106 MiB | 557 ms · 529 ms · 86 MiB | 789 ms · 790 ms · 125 MiB |
| COPY 1M rows | **214 ms** · **53 ms** · **0.7 KiB** | 240 ms · 116 ms · 305 KiB | — | 317 ms · 430 ms · 153 MiB ³ |
| 1M inserts in one transaction | **2.68 s** · **0.44 s** · **30 MiB** ⁴ | 3.65 s · 2.26 s · 932 MiB ⁴ | 69.7 s · 57.9 s · 244 MiB | 47.1 s · 28.9 s · 206 MiB |

Reading and copying a million rows take as long as the server takes to produce
or store them: a client that discards the rows of that read without decoding
them is no faster, at 620 to 690 ms. On wall time the three fastest are within
the noise of one another there. What a driver decides is what the client spends
on its side, and caduceus spends about half the CPU of the next and a fraction
of the memory.

Profiled, about 80% of caduceus's client CPU on a single query is the kernel's
`write` and `read` and the Go runtime waking for them; the driver's own code,
decoding included, is under a fifth.

Reproduce with:

```bash
cd benchmarks
DATABASE_URL='postgres://…' go test -run='^$' -bench=. -benchtime=3s -count=3 ./
DATABASE_URL='postgres://…' CADUCEUS_BENCH_1M=1 go test -run='^$' -bench=Million -benchtime=5x -timeout=0 ./
```

The benchmarks are a module of their own, so pgx, pglogrepl and lib/pq never
reach an application that imports caduceus.

## Packages

| Package | Contents |
| :--- | :--- |
| `caduceus` | `Conn`, `Rows`, `Row`, `Tx`, `BatchResults`, `PipelineResults`, `CopyFrom` |
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
