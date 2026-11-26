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
| **Protocol** | 3.2 with PostgreSQL 18 and later, whose cancel key is up to 256 bytes where 3.0's is 4; 3.0 with an older server: PostgreSQL 9.3 to 17 offers 3.0 in a NegotiateProtocolVersion. Something in front of the server that knows only 3.0 and refuses 3.2 outright fails the connection with `ErrProtocolVersion`, which names the setting to use: `max_protocol_version=3.0`. The driver does not dial again. `min_protocol_version` and `max_protocol_version` (`3.0`, `3.2`, `latest`) bound it, as in libpq 18 |
| **Query** | `Exec`, `Query`, `QueryRow`, `QueryInto`; parameters always bound server-side, never interpolated. `Exec` returns a `CommandTag`, whose `RowsAffected` counts the rows touched |
| **Results** | `CollectRows`, `AppendRows`, `CollectOneRow`, `CollectExactlyOneRow`, `ForEachRow`; rows into scalars (`RowTo`), maps (`RowToMap`) and structs by name or position (`RowToStructByName`, `…ByNameLax`, `…ByPos`, and `RowToAddrOf…` for pointers); `Rows.Values` decodes a row to Go values |
| **Transactions** | `Begin`, `BeginTx` with isolation level, read-only and deferrable; nested transactions as savepoints; idempotent `Commit` / `Rollback` |
| **Batch** | `SendBatch`: any number of queries as one transaction, in at most two round trips when they fit a 32 KiB window and a window at a time beyond that |
| **Pipeline** | `SendPipeline`: any number of statements sent without waiting for each reply, each failing on its own; inside a transaction, the fastest way to run many statements |
| **COPY** | `CopyFrom` streams rows in 64 KiB chunks, in binary where the column types allow it |
| **LISTEN/NOTIFY** | `WaitForNotification`, which never aborts a connection on timeout |
| **Cancellation** | a context ending mid-query sends a CancelRequest and closes the connection |
| **Prepared statements** | `Prepare` and `Tx.Prepare` return a `StatementDescription` (parameter OIDs, result fields); every way of running a statement runs a prepared one given its name, with no Parse; `Deallocate`, `DeallocateAll`. Kept apart from the statement cache, which never evicts them, and on the server whatever the options |
| **Statement cache** | LRU of server-side prepared statements, 64 by default. Off, for a pooler in transaction mode, nothing is kept on the server and a statement the connection has run before is still one round trip |
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

`Begin` sends nothing. `BEGIN` travels in front of the transaction's first
statement, in the same write, so a transaction takes one round trip fewer:
two for `BEGIN`, one statement and `COMMIT`, where three were sent. A
transaction that runs no statement sends nothing at all. The first statement
can be a batch or a copy as well: `BEGIN` goes in front of the batch, or of
whatever has to be described first. Only a pipeline of statements the
connection already knows sends `BEGIN` by itself, because its windows are
sent ahead of the answers.

The two share one fate. `BEGIN` is inside the statement's own Sync, or its own
Query string, and nothing else is sent until the server has answered for both.
So a `BEGIN` the server refuses, as it does a serializable one on a hot
standby, takes the statement down with it: the statement returns the server's
error without having run. From then until the transaction is settled, the `Tx`
and the connection itself return `ErrTxNotBegun` for everything, and
`conn.TxStatus()` reports a failed transaction. The caller believes one is
open, so nothing is let through to run, and commit, on its own.

PostgreSQL takes a transaction's snapshot at its first statement, not at
`BEGIN`, so what it sees is unchanged. Four things do differ from a `BEGIN`
sent at once:

- A connection that has died is found by the first statement rather than by
  `Begin`.
- `transaction_timestamp()` and `now()` are when that statement arrived.
- A `BEGIN` the server refuses is reported by the first statement, as above.
- A statement sent through `conn.Wire()` before the transaction's first
  statement runs outside the transaction: the protocol connection underneath
  knows nothing of a `BEGIN` still waiting.

A first statement the server cannot parse leaves what it always has, a
transaction open and failed for `Rollback` to end. Sent without arguments it
shares one string with `BEGIN`, and the server refuses such a string whole, so
it is sent again as its two parts. That is the one place the saved round trip
is paid back, and only on an error.

A `Tx` is spent once it is settled, a later transaction begins on its
connection, or the connection is unwound: every method then returns
`ErrTxClosed` and sends nothing, so a `Tx` kept past its transaction, or a
savepoint kept past the transaction around it, cannot act on a later one. A
transaction ended in SQL, by a `COMMIT` run as a statement, is not noticed,
and its `Tx` stays live.

A pool taking a connection back calls `conn.Unwind(ctx)`, which rolls back
whatever its caller left open, spends every `Tx` begun on it, and costs
nothing where the server has no transaction to end. `conn.TxStatus()` reports
a transaction begun and not yet sent as open, and one whose `BEGIN` was
refused as failed, so a pool that unwinds by that status catches both. Such a
pool still has to keep its own account of who holds a connection, because the
status of a transaction ended in SQL is idle.

### Results

```go
type User struct {
	ID    int64
	Login string  `db:"user_name"` // a tag names the column
	Email *string // NULL leaves it nil
	Audit         // embedded: its CreatedAt and CreatedBy are matched as User's own
}

rows, err := conn.Query(ctx, "SELECT id, user_name, email, created_at, created_by FROM users")
if err != nil {
	return err
}
users, err := caduceus.CollectRows(rows, caduceus.RowToStructByName[User])
```

Every collecting function closes the rows, whatever happens, so the connection
is free when it returns, and returns the error that ended them or the first one
the `RowToFunc` returned, with nothing collected. `CollectOneRow` takes the
first row and reads the rest off the connection undecoded; it and
`CollectExactlyOneRow` return `ErrNoRows` for none, and the latter
`ErrTooManyRows` for a second. `ForEachRow(rows, []any{&id, &name}, fn)` scans
each row into the same variables and returns the `CommandTag`.

| Function | Each row becomes |
| :--- | :--- |
| `RowTo[T]`, `RowToAddrOf[T]` | the one column scanned into a `T`, or a fresh `*T` |
| `RowToMap` | a fresh `map[string]any`, by column name, decoded as `Rows.Values` decodes it |
| `RowToStructByName[T]` | a struct, each column into the field of its name: its `db` tag exactly as written, or its Go name without regard to case or underscores, so `UserID` takes `user_id`. Every column needs a field and every field a column |
| `RowToStructByNameLax[T]` | the same, leaving a field without a column at its zero value |
| `RowToStructByPos[T]` | a struct, the n-th column into the n-th field; the counts must agree |
| `RowToAddrOfStruct…[T]` | a fresh `*T` of each of the three |

Only exported fields take columns and `db:"-"` skips one. An embedded struct is
flattened into its parent, tagged or not, as pgx flattens it, except that
`db:"-"` skips it too, where pgx flattens it regardless. A struct's own field
shadows an embedded one of the same name, as in Go: `type User struct{ Base; ID
int64 }` puts the `id` column into `User.ID`, and `Base.ID`, shadowed, is not
wanted a column of its own. Which field takes which column is derived once for
each struct type, mapping and set of column names, and kept for the next row
and the next query, in a cache safe for concurrent use. Only a mapping that
works is kept, a by-name one has no more names than its struct has fields, a
by-position one keeps no names, and the cache holds at most about 1024 of them
and 1 MiB of their column names: a program handed column names it does not
choose is never made to hold more, as the cache is emptied each time it fills.
`Values`, `RowToMap` and the scalar and struct functions all copy what they
keep, so what they return stays valid after the next row is read.

### Prepared statements

```go
sd, err := conn.Prepare(ctx, "user_by_id", "SELECT id, login FROM users WHERE id = $1")
// sd.ParamOIDs, sd.Fields: what the server said of the statement.
row := conn.QueryRow(ctx, "user_by_id", 42) // Bind and Execute, no Parse
```

The name stands in for the SQL in `Exec`, `Query`, `QueryRow`, `QueryInto`,
`SendBatch` and `SendPipeline`, on the connection and on a `Tx`. An empty name
names the statement by its text. Preparing a name again with the same text
costs nothing; with another text the server refuses it with 42P05, as in pgx,
and the statement already prepared under the name stays and keeps running by
it: `Deallocate` the name first to give it another text. A statement the server reports out of date, with
0A000 after a table changed under it, keeps failing until it is prepared
again, which then parses it afresh; the driver never closes it on its own.
`Deallocate` closes one, and is no error for a name not prepared;
`DeallocateAll` closes them all and forgets the statement cache as well.
Nothing bounds how many a connection holds: each costs memory in the client
and in the server backend, for every connection of a pool. SQL built at run
time belongs in the statement cache, which is capped, not in
`Prepare(ctx, "", sql)`, unless each such `Prepare` is paired with a
`Deallocate`.

The cache is separate: it names its statements itself, never evicts a
caller's and never takes a caller's name, even one that looks like its own.
A name longer than the server's 63 bytes, or one that is the SQL text, is
given a digest name on the server, which `StatementDescription.Name` reports.
Parse is not transactional: a statement prepared in a transaction that rolls
back stays prepared, and `Tx.Prepare` sends the transaction's waiting `BEGIN`
along with its Parse. Behind a pooler in transaction mode a statement lives on
one server connection only, so prepare and run it inside one transaction.

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

### Behind a pooler

A pooler in transaction mode, such as PgBouncer with `pool_mode = transaction`,
lends a client a server connection for one transaction at a time, and outside
a transaction that is for one Sync. A prepared statement lives on one server
connection, so the statement cache is only for a pooler that follows prepared
statements from one to the next, as PgBouncer does from 1.21 with
`max_prepared_statements` set. Behind any other, turn it off:

```go
conn, err := caduceus.Connect(ctx, dsn, caduceus.Options{StatementCacheCapacity: -1})
```

Nothing on the server then outlives a Sync. A statement is parsed into the
unnamed statement in the same Sync that binds and runs it, so it makes no
difference which server connection the pooler lends for it. A batch is one
Sync from end to end, and every statement of a pipeline has its own.

A statement the connection has run before is still one round trip. The first
time, the server is asked what the statement takes and returns, and the
connection remembers the answer, for 64 statements unless
`Options.DescriptionCacheCapacity` says otherwise; a negative capacity
remembers nothing and asks every time. What is remembered is held to the
server's word:

- The statement is parsed with the parameter types it was described with, so
  the server reads the arguments as they were encoded. A prepared statement
  keeps the types it was prepared with in just the same way.
- The server describes the columns again every time the statement runs, and
  they are compared with what was remembered. If a table was altered in
  between, the result is read as the server describes it now, and the
  statement is asked about afresh the next time. `Rows.Fields` asked for
  before the first `Next` reads as far as that description, which is already
  on its way, so the columns it gives are this execution's.
- The result was asked for in the formats that suited the columns
  remembered, so a column that changed type arrives in the format its old
  type was read in. A numeric, the commonest change, is read in binary as
  the server prints it. A type that cannot be read in that format is refused
  when it is scanned with `ErrResultChanged`, which says the statement ran:
  a write that returned it was made.
- An error before the server has bound the statement forgets the
  description: the server did not take the statement, or an argument, as
  encoded for the types remembered, whether it says so with class 42, an
  invalid value or a type that no longer exists. An argument that cannot be
  encoded for the type remembered does too. An error the statement raises as
  it runs is its own, and leaves the description be.

What this costs against the cache is the server's work, since an unnamed
statement is parsed and planned every time it runs. A pipeline outside a
transaction has every statement parsed for that reason, each being a Sync of
its own; in a batch, or in a pipeline inside a transaction, a run of the same
statement shares one Parse.

`Prepare` keeps its statement on the server whatever the options, so behind
such a pooler a prepared statement is safe only within the transaction that
prepared it: the next transaction may be lent another server connection,
which has no such statement or, worse, another client's of the same name.

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
| QueryRow, one int8 parameter | **49.2 µs** | 65.3 µs | 67.6 µs | 108 µs |
| QueryRow, nothing kept on the server ⁵ | **65.0 µs** | 80.2 µs | — | — |
| 100 rows × 4 typed columns | **171 µs** | 260 µs | 249 µs | 350 µs |
| BEGIN, INSERT, COMMIT | **122 µs** | 232 µs | 270 µs | 286 µs |
| BEGIN, batch of 10 inserts, COMMIT | **158 µs** | 264 µs | 910 µs ¹ | 1.33 ms ¹ |
| Batch of 10 selects | **79.4 µs** | 110 µs | 728 µs ¹ | 1.15 ms ¹ |
| COPY 1,000 rows | **1.96 ms** | 2.32 ms | — ² | 3.06 ms ³ |
| 1,000 inserts in one transaction | **3.05 ms** ⁴ | 4.80 ms ⁴ | 79 ms | 54 ms |
| Decode a pgoutput UPDATE | **1.0 µs** | 1.8 µs (pglogrepl) | — | — |

Wall time includes the server's share of every round trip, which no driver can
change. Client CPU per operation is the driver's own (`cpu-ns/op` in the
benchmark output, the process's user and system time):

| Workload | caduceus | pgx v5.10 | database/sql + pgx | database/sql + lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| QueryRow | **31.0 µs** | 55.1 µs | 57.6 µs | 68.3 µs |
| QueryRow, nothing kept on the server | **36.0 µs** | 58.2 µs | — | — |
| 100 rows × 4 typed columns | **73.2 µs** | 168 µs | 153 µs | 203 µs |
| BEGIN, INSERT, COMMIT | **70.2 µs** | 182 µs | 227 µs | 219 µs |
| BEGIN, batch of 10 inserts, COMMIT | **76.6 µs** | 192 µs | 758 µs | 843 µs |
| Batch of 10 selects | **42.3 µs** | 87.1 µs | 622 µs | 723 µs |
| COPY 1,000 rows | **266 µs** | 650 µs | — | 1.19 ms |
| 1,000 inserts in one transaction | **544 µs** | 2.62 ms | 65 ms | 35 ms |

Memory allocated per operation:

| Workload | caduceus | pgx v5.10 | database/sql + pgx | database/sql + lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| QueryRow | **263 B** · 1 alloc | 351 B · 5 | 759 B · 15 | 703 B · 17 |
| QueryRow, nothing kept on the server | **263 B** · 1 | 359 B · 6 | — | — |
| 100 rows × 4 typed columns | **1.9 KiB** · 105 | 11 KiB · 662 | 8.2 KiB · 525 | 12 KiB · 623 |
| BEGIN, INSERT, COMMIT | **55 B** · 1 | 192 B · 6 | 724 B · 16 | 767 B · 17 |
| BEGIN, batch of 10 inserts, COMMIT | **440 B** · 4 | 6.2 KiB · 116 | 2.9 KiB · 70 | 4.0 KiB · 98 |
| Batch of 10 selects | **2.6 KiB** · 12 | 6.7 KiB · 161 | 7.3 KiB · 140 | 6.7 KiB · 160 |
| COPY 1,000 rows | **720 B** · 21 | 115 KiB · 43 | — | 221 KiB · 4,027 |
| 1,000 inserts in one transaction | **536 B** · 8 | 809 KiB · 9,037 | 252 KiB · 6,033 | 204 KiB · 7,014 |

¹ database/sql has no batch, so the ten queries run one after another.
² pgx's database/sql driver has no COPY.
³ lib/pq's `CopyIn`, which needs a transaction around it.
⁴ `tx.SendPipeline` for caduceus, `SendBatch` for pgx.
⁵ The statement cache off, as behind a pooler in transaction mode: one round
trip, its description remembered on the client. pgx's fastest such mode,
`QueryExecModeCacheDescribe`, is the one shown; `QueryExecModeExec` takes
83.0 µs, and caduceus remembering nothing, asking every time, 120 µs.

### A million rows

Each cell is wall time · client CPU · memory allocated, for one pass over a
million rows: reading four typed columns, copying two columns in, and inserting
two columns with a statement per row in one transaction. Means of five passes
on the same machine.

| Workload | caduceus | pgx v5.10 | database/sql + pgx | lib/pq |
| :--- | ---: | ---: | ---: | ---: |
| Read 1M rows | **559 ms** · **273 ms** · **15 MiB** | 646 ms · 627 ms · 106 MiB | 654 ms · 586 ms · 86 MiB | 818 ms · 808 ms · 125 MiB |
| COPY 1M rows | **216 ms** · **46 ms** · **57 KiB** | 263 ms · 114 ms · 319 KiB | — | 354 ms · 437 ms · 153 MiB ³ |
| 1M inserts in one transaction | **2.56 s** · **0.48 s** · **70 KiB** ⁴ | 3.74 s · 2.27 s · 932 MiB ⁴ | 71.3 s · 59.1 s · 244 MiB | 54.2 s · 35.4 s · 206 MiB |

Reading and copying a million rows take as long as the server takes to produce
or store them: a client that discards the rows of that read without decoding
them is no faster, at 620 to 690 ms. On wall time the three fastest are close
there. What a driver decides is what the client spends
on its side, and caduceus spends about half the CPU of the next and a fraction
of the memory.

Profiled, about 80% of caduceus's client CPU on a single query is the kernel's
`write` and `read` and the Go runtime waking for them; the driver's own code,
decoding included, is under a fifth.

On disk, a program that connects and runs one query builds to 5.2 MiB with
caduceus, against 8.8 MiB with pgx, 8.9 MiB with database/sql over pgx and
5.3 MiB with database/sql over lib/pq (linux/amd64, `CGO_ENABLED=0`,
`-trimpath -ldflags='-s -w'`).

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
| `caduceus` | `Conn`, `Rows`, `Row`, `Tx`, `BatchResults`, `PipelineResults`, `CopyFrom`, `CommandTag`, `CollectRows` and the `RowTo…` functions |
| `caduceus/wire` | the frontend/backend protocol, version 3.0: config, TLS, auth, framing, errors, cancel, notifications, CopyBoth |
| `caduceus/codec` | Go values to and from PostgreSQL's text and binary formats |
| `caduceus/replication` | logical replication: pgoutput decoding, stream messages, LSNs |

## Testing

```bash
go test -race ./...                                   # no database needed
DATABASE_URL='postgres://…' go test -race ./...       # adds the live tests
```

Two more variables add tests that need more than one server. `POOLER_URL`
names a pooler in transaction mode in front of the same server, for the tests
of what such a pooler does to a statement. `STANDBY_URL` names a hot standby,
which is the one place a server refuses a `BEGIN`.

## License

MIT
