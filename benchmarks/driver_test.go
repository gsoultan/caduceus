// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package benchmarks

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/gsoultan/caduceus"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
)

// caduceus against its competitors, each on one connection with one caller,
// so a number is the driver's cost per operation with no pool or queueing in
// it. database/sql runs twice, over pgx's stdlib driver and over lib/pq, with
// its pool held to one connection.
//
// "SELECT 1" barely touches a driver, so these workloads bind parameters,
// decode typed rows, run transactions, pipeline batches and copy rows, which
// is where a driver spends its time.

const rowsSQL = `SELECT g, 'customer-' || g, now(), g * 1.5 FROM generate_series(1, $1::int4) AS g`

func dsn(b *testing.B) string {
	b.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		b.Skip("DATABASE_URL not set")
	}
	return url
}

type contender struct {
	name string
	run  func(b *testing.B, ctx context.Context)
}

func scanRows(b *testing.B, next func() bool, scan func(...any) error) int {
	var (
		id     int64
		name   string
		at     time.Time
		amount float64
	)
	// One destination list for the whole result, so the benchmark measures
	// the driver's allocations rather than its own.
	dest := []any{&id, &name, &at, &amount}
	n := 0
	for next() {
		if err := scan(dest...); err != nil {
			b.Fatal(err)
		}
		n++
	}
	return n
}

// sqlPool is the database/sql side of a benchmark: the same closures run
// once over pgx's stdlib driver and once over lib/pq, with DB switched between
// them by runAll.
type sqlPool struct {
	*sql.DB
	drivers map[string]*sql.DB
}

// sqlDrivers are the database/sql drivers measured, by the name each result
// is reported under.
var sqlDrivers = map[string]string{"database-sql-pgx": "pgx", "lib-pq": "postgres"}

var currentSQL *sqlPool

func connections(b *testing.B) (*caduceus.Conn, *pgx.Conn, *sqlPool) {
	b.Helper()
	ctx := b.Context()
	url := dsn(b)
	cad, err := caduceus.Connect(ctx, url, caduceus.Options{})
	if err != nil {
		b.Fatal(err)
	}
	px, err := pgx.Connect(ctx, url)
	if err != nil {
		b.Fatal(err)
	}
	pool := &sqlPool{drivers: map[string]*sql.DB{}}
	for name, driver := range sqlDrivers {
		db, err := sql.Open(driver, url)
		if err != nil {
			b.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if err := db.PingContext(ctx); err != nil {
			b.Fatal(err)
		}
		pool.drivers[name] = db
		b.Cleanup(func() { _ = db.Close() })
	}
	currentSQL = pool
	b.Cleanup(func() {
		_ = cad.Close(context.Background())
		_ = px.Close(context.Background())
	})
	return cad, px, pool
}

// pqCopy copies rows the way lib/pq does a COPY: a statement prepared from
// CopyIn inside a transaction, one Exec per row, and an Exec with no arguments
// to send what is buffered.
func pqCopy(ctx context.Context, db *sql.DB, table string, columns []string, rows [][]any) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn(table, columns...))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			return err
		}
	}
	if _, err := stmt.ExecContext(ctx); err != nil {
		return err
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

// processCPU is the CPU time this process has used, user and system, in
// nanoseconds.
func processCPU() int64 {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return usage.Utime.Nano() + usage.Stime.Nano()
}

func runAll(b *testing.B, contenders []contender) {
	var expanded []contender
	for _, c := range contenders {
		if c.name != "database-sql" {
			expanded = append(expanded, c)
			continue
		}
		for _, name := range []string{"database-sql-pgx", "lib-pq"} {
			run := c.run
			expanded = append(expanded, contender{name, func(b *testing.B, ctx context.Context) {
				currentSQL.DB = currentSQL.drivers[name]
				run(b, ctx)
			}})
		}
	}
	for _, c := range expanded {
		b.Run(c.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			start := processCPU()
			c.run(b, ctx)
			// Wall time includes the server's share of every round trip, which
			// no driver can change. Client CPU per operation is the driver's own.
			b.ReportMetric(float64(processCPU()-start)/float64(b.N), "cpu-ns/op")
		})
	}
}

func BenchmarkQueryRow(b *testing.B) {
	cad, px, db := connections(b)
	var got int64
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				if err := cad.QueryRow(ctx, "SELECT $1::int8 + 1", int64(i)).Scan(&got); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				if err := px.QueryRow(ctx, "SELECT $1::int8 + 1", int64(i)).Scan(&got); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				if err := db.QueryRowContext(ctx, "SELECT $1::int8 + 1", int64(i)).Scan(&got); err != nil {
					b.Fatal(err)
				}
			}
		}},
	})
}

// BenchmarkQueryRowNothingKept is BenchmarkQueryRow in the mode each driver
// has for a pooler in transaction mode that does not follow prepared
// statements: nothing kept on the server from one Sync to the next.
//
// caduceus and pgx's cache_describe remember what the server said of a
// statement and send its Parse with its Bind, one round trip. pgx's exec does
// not ask at all, and sends every argument as text of a type it guesses from
// the Go value. The last two ask every time, which is two round trips; pgx's
// describe_exec binds in the second what it parsed in the first, which is the
// thing such a pooler breaks, and is here for what asking costs.
func BenchmarkQueryRowNothingKept(b *testing.B) {
	ctx := b.Context()
	url := dsn(b)
	var contenders []contender
	var got int64
	for _, mode := range []struct {
		name    string
		options caduceus.Options
	}{
		{"caduceus", caduceus.Options{StatementCacheCapacity: -1}},
		{"caduceus-asking-every-time", caduceus.Options{StatementCacheCapacity: -1, DescriptionCacheCapacity: -1}},
	} {
		conn, err := caduceus.Connect(ctx, url, mode.options)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = conn.Close(context.Background()) })
		contenders = append(contenders, contender{mode.name, func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				if err := conn.QueryRow(ctx, "SELECT $1::int8 + 1", int64(i)).Scan(&got); err != nil {
					b.Fatal(err)
				}
			}
		}})
	}
	for _, mode := range []struct {
		name string
		mode pgx.QueryExecMode
	}{
		{"pgx-cache-describe", pgx.QueryExecModeCacheDescribe},
		{"pgx-exec", pgx.QueryExecModeExec},
		{"pgx-describe-exec", pgx.QueryExecModeDescribeExec},
	} {
		config, err := pgx.ParseConfig(url)
		if err != nil {
			b.Fatal(err)
		}
		config.DefaultQueryExecMode = mode.mode
		conn, err := pgx.ConnectConfig(ctx, config)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = conn.Close(context.Background()) })
		contenders = append(contenders, contender{mode.name, func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				if err := conn.QueryRow(ctx, "SELECT $1::int8 + 1", int64(i)).Scan(&got); err != nil {
					b.Fatal(err)
				}
			}
		}})
	}
	// The order they were added in is the order they run in.
	runAll(b, []contender{contenders[0], contenders[2], contenders[3], contenders[1], contenders[4]})
}

func BenchmarkRows100(b *testing.B) {
	cad, px, db := connections(b)
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				rows, err := cad.Query(ctx, rowsSQL, int32(100))
				if err != nil {
					b.Fatal(err)
				}
				if scanRows(b, rows.Next, rows.Scan) != 100 || rows.Err() != nil {
					b.Fatal(rows.Err())
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				rows, err := px.Query(ctx, rowsSQL, int32(100))
				if err != nil {
					b.Fatal(err)
				}
				if scanRows(b, rows.Next, rows.Scan) != 100 || rows.Err() != nil {
					b.Fatal(rows.Err())
				}
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				rows, err := db.QueryContext(ctx, rowsSQL, int32(100))
				if err != nil {
					b.Fatal(err)
				}
				if scanRows(b, rows.Next, rows.Scan) != 100 || rows.Err() != nil {
					b.Fatal(rows.Err())
				}
				rows.Close()
			}
		}},
	})
}

func benchTable(b *testing.B, cad *caduceus.Conn) string {
	b.Helper()
	name := "caduceus_bench_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := cad.Exec(b.Context(), "CREATE UNLOGGED TABLE "+name+" (id int8, note text)"); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _, _ = cad.Exec(context.Background(), "DROP TABLE "+name) })
	return name
}

// A transaction of one insert: BEGIN, INSERT with parameters, COMMIT.
func BenchmarkTransaction(b *testing.B) {
	cad, px, db := connections(b)
	table := benchTable(b, cad)
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				tx, err := cad.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := tx.Exec(ctx, insert, int64(i), "note"); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				tx, err := px.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := tx.Exec(ctx, insert, int64(i), "note"); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for i := 0; b.Loop(); i++ {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := tx.ExecContext(ctx, insert, int64(i), "note"); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		}},
	})
}

// A transaction around a batch: BEGIN, ten inserts sent together, COMMIT.
// database/sql has no batch, so it runs the inserts one after another.
func BenchmarkTransactionBatch10(b *testing.B) {
	cad, px, db := connections(b)
	table := benchTable(b, cad)
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			queries := make([]caduceus.Query, 10)
			for i := range queries {
				queries[i] = caduceus.Query{SQL: insert, Arguments: []any{int64(i), "note"}}
			}
			for b.Loop() {
				tx, err := cad.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				results, err := tx.SendBatch(ctx, queries)
				if err != nil {
					b.Fatal(err)
				}
				if err := results.Close(); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				tx, err := px.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				batch := &pgx.Batch{}
				for i := range 10 {
					batch.Queue(insert, int64(i), "note")
				}
				if err := tx.SendBatch(ctx, batch).Close(); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				for i := range 10 {
					if _, err := tx.ExecContext(ctx, insert, int64(i), "note"); err != nil {
						b.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		}},
	})
}

// Ten parameterised selects in one round trip. database/sql has no batch, so
// it runs them one after another, which is what an application on it does.
func BenchmarkBatch10(b *testing.B) {
	cad, px, db := connections(b)
	const query = "SELECT $1::int8 * 2"
	var got int64
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			queries := make([]caduceus.Query, 10)
			for i := range queries {
				queries[i] = caduceus.Query{SQL: query, Arguments: []any{int64(i)}}
			}
			for b.Loop() {
				results, err := cad.SendBatch(ctx, queries)
				if err != nil {
					b.Fatal(err)
				}
				for range queries {
					if err := results.QueryRow().Scan(&got); err != nil {
						b.Fatal(err)
					}
				}
				if err := results.Close(); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				batch := &pgx.Batch{}
				for i := range 10 {
					batch.Queue(query, int64(i))
				}
				results := px.SendBatch(ctx, batch)
				for range 10 {
					if err := results.QueryRow().Scan(&got); err != nil {
						b.Fatal(err)
					}
				}
				if err := results.Close(); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				for i := range 10 {
					if err := db.QueryRowContext(ctx, query, int64(i)).Scan(&got); err != nil {
						b.Fatal(err)
					}
				}
			}
		}},
	})
}

// A thousand rows into a table with each driver's COPY. lib/pq's is CopyIn,
// reached through database/sql; pgx's database/sql driver has none, and the
// same rows as INSERT statements are BenchmarkInsert1000InTransaction.
func BenchmarkCopy1000(b *testing.B) {
	cad, px, db := connections(b)
	table := benchTable(b, cad)
	rows := make([][]any, 1000)
	for i := range rows {
		rows[i] = []any{int64(i), "row " + strconv.Itoa(i)}
	}
	columns := []string{"id", "note"}
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				if n, err := cad.CopyFrom(ctx, []string{table}, columns, caduceus.CopyFromRows(rows)); err != nil || n != 1000 {
					b.Fatal(n, err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				if n, err := px.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows)); err != nil || n != 1000 {
					b.Fatal(n, err)
				}
			}
		}},
		{"lib-pq-copyin", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				if err := pqCopy(ctx, db.drivers["lib-pq"], table, columns, rows); err != nil {
					b.Fatal(err)
				}
			}
		}},
	})
}

// A thousand INSERT statements, each with its own parameters, in one
// transaction. caduceus pipelines them and pgx batches them, both without
// waiting for each reply; database/sql can only run them one after another.
func BenchmarkInsert1000InTransaction(b *testing.B) {
	cad, px, db := connections(b)
	table := benchTable(b, cad)
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"
	rows := make([][]any, 1000)
	queries := make([]caduceus.Query, len(rows))
	for i := range rows {
		rows[i] = []any{int64(i), "row " + strconv.Itoa(i)}
		queries[i] = caduceus.Query{SQL: insert, Arguments: rows[i]}
	}
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				tx, err := cad.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				results, err := tx.SendPipeline(ctx, queries)
				if err != nil {
					b.Fatal(err)
				}
				if err := results.Close(); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				tx, err := px.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				batch := &pgx.Batch{}
				for _, row := range rows {
					batch.Queue(insert, row...)
				}
				if err := tx.SendBatch(ctx, batch).Close(); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				stmt, err := tx.PrepareContext(ctx, insert)
				if err != nil {
					b.Fatal(err)
				}
				for _, row := range rows {
					if _, err := stmt.ExecContext(ctx, row...); err != nil {
						b.Fatal(err)
					}
				}
				_ = stmt.Close()
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		}},
	})
}
