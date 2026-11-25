// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// sentResults is what a batch and a pipeline both give back.
type sentResults interface {
	Query() (*Rows, error)
	QueryRow() *Row
	Close() error
}

// inTransaction ends its transaction as its results are closed.
type inTransaction struct {
	*PipelineResults
	tx *Tx
}

func (r inTransaction) Close() error {
	return errors.Join(r.PipelineResults.Close(), r.tx.Rollback(context.Background()))
}

// A statement larger than a window is sent in a window of its own, and only
// once every result in front of it has been read. It used to be written
// behind a window whose results were not read yet: a statement whose rows
// fill the socket's buffers in front of one whose text or argument does, and
// the server stopped reading to wait for its rows to be taken while the
// client stopped reading to finish writing, each waiting on the other for
// good.
func TestLiveOversizedStatementWaitsForTheResultsInFrontOfIt(t *testing.T) {
	const rowsInFront = 50_000
	wide := "SELECT repeat('x', 1000) FROM generate_series(1, " + "50000" + ")"
	huge := strings.Repeat("a", 16<<20)
	statements := map[string]Query{
		"a long text":     {SQL: "SELECT length('" + huge + "')"},
		"a long argument": {SQL: "SELECT length($1::text)", Arguments: []any{huge}},
	}
	sends := map[string]func(ctx context.Context, conn *Conn, queries []Query) (sentResults, error){
		"a batch": func(ctx context.Context, conn *Conn, queries []Query) (sentResults, error) {
			return conn.SendBatch(ctx, queries)
		},
		"a pipeline": func(ctx context.Context, conn *Conn, queries []Query) (sentResults, error) {
			return conn.SendPipeline(ctx, queries)
		},
		"a pipeline in a transaction": func(ctx context.Context, conn *Conn, queries []Query) (sentResults, error) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return nil, err
			}
			results, err := tx.SendPipeline(ctx, queries)
			if err != nil {
				return nil, err
			}
			return inTransaction{results, tx}, nil
		},
	}
	for _, options := range append([]Options{{}}, notKept...) {
		for sendName, send := range sends {
			for statementName, oversized := range statements {
				for _, known := range []bool{false, true} {
					name := sendName + ", " + statementName
					if known {
						name += ", known"
					}
					t.Run(name, func(t *testing.T) {
						conn := liveConn(t, options)
						ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
						defer cancel()
						queries := []Query{{SQL: wide}, oversized, {SQL: "SELECT 2"}}
						if known {
							// Once more, with each statement remembered.
							results, err := send(ctx, conn, queries)
							if err != nil {
								t.Fatal(err)
							}
							if err := results.Close(); err != nil {
								t.Fatal(err)
							}
						}
						results, err := send(ctx, conn, queries)
						if err != nil {
							t.Fatal(err)
						}
						rows, err := results.Query()
						if err != nil {
							t.Fatal(err)
						}
						n := 0
						for rows.Next() {
							n++
						}
						rows.Close()
						if err := rows.Err(); err != nil || n != rowsInFront {
							t.Fatalf("%d rows in front, %v; want %d", n, err, rowsInFront)
						}
						var length, two int
						if err := results.QueryRow().Scan(&length); err != nil || length != len(huge) {
							t.Fatalf("the oversized statement = %d, %v; want %d", length, err, len(huge))
						}
						if err := results.QueryRow().Scan(&two); err != nil || two != 2 {
							t.Fatalf("the statement after it = %d, %v; want 2", two, err)
						}
						if err := results.Close(); err != nil {
							t.Fatal(err)
						}
					})
				}
			}
		}
	}
}
