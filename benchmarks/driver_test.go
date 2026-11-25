// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package benchmarks

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/caduceus"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// caduceus against its competitors, each on one connection with one caller,
// so a number is the driver's cost per operation with no pool or queueing in
// it. database/sql runs over pgx's stdlib driver, the usual way to reach
// PostgreSQL through database/sql, with its pool held to one connection.
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

func connections(b *testing.B) (*caduceus.Conn, *pgx.Conn, *sql.DB) {
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
	db, err := sql.Open("pgx", url)
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = cad.Close(context.Background())
		_ = px.Close(context.Background())
		_ = db.Close()
	})
	return cad, px, db
}

func runAll(b *testing.B, contenders []contender) {
	for _, c := range contenders {
		b.Run(c.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			c.run(b, ctx)
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

// A thousand rows into a table with COPY. database/sql has no COPY, so it
// inserts them in one transaction with a prepared statement.
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
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+table+" VALUES ($1, $2)")
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
