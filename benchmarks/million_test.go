// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package benchmarks

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/gsoultan/caduceus"
	"github.com/jackc/pgx/v5"
)

// A million rows each way: read, copied in, and inserted one statement per
// row. One iteration is seconds long, so these run only when
// CADUCEUS_BENCH_1M is set:
//
//	CADUCEUS_BENCH_1M=1 go test -run='^$' -bench=Million -benchtime=3x ./

const million = 1_000_000

func millionOnly(b *testing.B) {
	b.Helper()
	if os.Getenv("CADUCEUS_BENCH_1M") == "" {
		b.Skip("set CADUCEUS_BENCH_1M=1 to run the million-row benchmarks")
	}
}

func BenchmarkMillionRowsRead(b *testing.B) {
	millionOnly(b)
	cad, px, db := connections(b)
	check := func(b *testing.B, n int, err error) {
		if err != nil || n != million {
			b.Fatal(n, err)
		}
	}
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				rows, err := cad.Query(ctx, rowsSQL, int32(million))
				if err != nil {
					b.Fatal(err)
				}
				check(b, scanRows(b, rows.Next, rows.Scan), rows.Err())
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				rows, err := px.Query(ctx, rowsSQL, int32(million))
				if err != nil {
					b.Fatal(err)
				}
				check(b, scanRows(b, rows.Next, rows.Scan), rows.Err())
			}
		}},
		{"database-sql", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				rows, err := db.QueryContext(ctx, rowsSQL, int32(million))
				if err != nil {
					b.Fatal(err)
				}
				check(b, scanRows(b, rows.Next, rows.Scan), rows.Err())
				rows.Close()
			}
		}},
	})
}

func millionRows() [][]any {
	rows := make([][]any, million)
	for i := range rows {
		rows[i] = []any{int64(i), "row " + strconv.Itoa(i)}
	}
	return rows
}

func BenchmarkMillionRowsCopy(b *testing.B) {
	millionOnly(b)
	cad, px, db := connections(b)
	table := benchTable(b, cad)
	rows := millionRows()
	columns := []string{"id", "note"}
	truncate := func(b *testing.B) {
		b.StopTimer()
		if _, err := cad.Exec(context.Background(), "TRUNCATE "+table); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				truncate(b)
				if n, err := cad.CopyFrom(ctx, []string{table}, columns, caduceus.CopyFromRows(rows)); err != nil || n != million {
					b.Fatal(n, err)
				}
			}
		}},
		{"pgx", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				truncate(b)
				if n, err := px.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows)); err != nil || n != million {
					b.Fatal(n, err)
				}
			}
		}},
		{"lib-pq-copyin", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				truncate(b)
				if err := pqCopy(ctx, db.drivers["lib-pq"], table, columns, rows); err != nil {
					b.Fatal(err)
				}
			}
		}},
	})
}

// A million INSERT statements, each with its own parameters, in one
// transaction. caduceus pipelines them; pgx sends them as one batch;
// database/sql has neither, so it runs a prepared statement a million times.
func BenchmarkMillionRowsInsert(b *testing.B) {
	millionOnly(b)
	cad, px, db := connections(b)
	table := benchTable(b, cad)
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"
	rows := millionRows()
	queries := make([]caduceus.Query, million)
	for i, row := range rows {
		queries[i] = caduceus.Query{SQL: insert, Arguments: row}
	}
	truncate := func(b *testing.B) {
		b.StopTimer()
		if _, err := cad.Exec(context.Background(), "TRUNCATE "+table); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	runAll(b, []contender{
		{"caduceus", func(b *testing.B, ctx context.Context) {
			for b.Loop() {
				truncate(b)
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
				truncate(b)
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
				truncate(b)
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
