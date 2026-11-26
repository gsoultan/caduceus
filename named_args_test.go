// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// A placeholder is rewritten only where SQL would read it as one: not in a
// string, an escape string, a quoted identifier, a comment or a
// dollar-quoted string, and not in an operator such as @> or @@. A name used
// twice is one parameter, numbered in the order of first use.
func TestRewriteNamed(t *testing.T) {
	values := map[string]any{"a": 1, "b": "two", "user_id": 3}
	tests := []struct {
		name     string
		sql      string
		want     string
		wantArgs []any
	}{
		{"plain", "SELECT @a, @b", "SELECT $1, $2", []any{1, "two"}},
		{"reused", "SELECT @b, @a, @b", "SELECT $1, $2, $1", []any{"two", 1}},
		{"underscore and end of text", "WHERE id = @user_id", "WHERE id = $1", []any{3}},
		{"missing binds NULL", "SELECT @c", "SELECT $1", []any{nil}},
		{"string", "SELECT '@a', @a", "SELECT '@a', $1", []any{1}},
		{"doubled quote in a string", "SELECT 'it''s @a', @b", "SELECT 'it''s @a', $1", []any{"two"}},
		{"escape string", `SELECT E'\' @a', @b`, `SELECT E'\' @a', $1`, []any{"two"}},
		{"backslash in a plain string", `SELECT '\', @a`, `SELECT '\', $1`, []any{1}},
		{"quoted identifier", `SELECT "@a" FROM t WHERE x = @a`, `SELECT "@a" FROM t WHERE x = $1`, []any{1}},
		{"line comment", "SELECT 1 -- @a\n, @b", "SELECT 1 -- @a\n, $1", []any{"two"}},
		{"nested block comment", "SELECT /* @a /* @a */ @a */ @b", "SELECT /* @a /* @a */ @a */ $1", []any{"two"}},
		{"dollar quote", "SELECT $$ @a $$, @b", "SELECT $$ @a $$, $1", []any{"two"}},
		{"tagged dollar quote", "SELECT $x$ @a $y$ @a $x$, @b", "SELECT $x$ @a $y$ @a $x$, $1", []any{"two"}},
		{"dollar in an identifier is no quote", "SELECT a$b$ FROM t WHERE x = @a", "SELECT a$b$ FROM t WHERE x = $1", []any{1}},
		{"operators", "SELECT j @> '{}' AND t @@ q AND @-1 < @a", "SELECT j @> '{}' AND t @@ q AND @-1 < $1", []any{1}},
		{"at sign at the end", "SELECT @", "SELECT @", nil},
		{"unterminated string", "SELECT '@a", "SELECT '@a", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args, err := rewriteNamed(tt.sql, values, false)
			if err != nil {
				t.Fatal(err)
			}
			if sql != tt.want || !slices.Equal(args, tt.wantArgs) {
				t.Fatalf("rewriteNamed(%q) = %q, %v; want %q, %v", tt.sql, sql, args, tt.want, tt.wantArgs)
			}
		})
	}
}

// StrictNamedArgs refuses a name its map lacks, which NamedArgs binds as
// NULL, and a value the statement never names: both are more often a typo
// than intended.
func TestStrictNamedArgs(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		args StrictNamedArgs
		ok   bool
	}{
		{"match", "SELECT @a, @a, @b", StrictNamedArgs{"a": 1, "b": 2}, true},
		{"missing", "SELECT @a, @b", StrictNamedArgs{"a": 1}, false},
		{"unused", "SELECT @a", StrictNamedArgs{"a": 1, "b": 2}, false},
		{"named only in a string", "SELECT '@a'", StrictNamedArgs{"a": 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := tt.args.RewriteQuery(context.Background(), nil, tt.sql, nil)
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, ErrNamedArgs)) {
				t.Fatalf("RewriteQuery(%q) = %v", tt.sql, err)
			}
		})
	}
}

// Named arguments reach the server as bound parameters on every path that
// runs a statement, a transaction's and a batch's and a pipeline's included,
// with the statement cache on and off. The caller's batch is left as it was.
func TestLiveNamedArgs(t *testing.T) {
	for _, options := range append([]Options{{}}, notKept...) {
		conn := liveConn(t, options)
		ctx := t.Context()
		args := NamedArgs{"n": int64(41), "s": "it's"}
		const sql = "SELECT @n::int8 + 1, @s::text, @n::int8"
		check := func(name string, row interface{ Scan(...any) error }) {
			t.Helper()
			var n, again int64
			var s string
			if err := row.Scan(&n, &s, &again); err != nil || n != 42 || s != "it's" || again != 41 {
				t.Fatalf("%s: %d, %q, %d, %v", name, n, s, again, err)
			}
		}
		check("QueryRow", conn.QueryRow(ctx, sql, args))

		rows, err := conn.Query(ctx, sql, args)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		check("Query", rows)
		rows.Close()

		tag, err := conn.Exec(ctx, "SELECT @n::int8", args)
		if err != nil || tag.String() != "SELECT 1" {
			t.Fatalf("Exec = %q, %v", tag, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check("Tx.QueryRow", tx.QueryRow(ctx, sql, args))
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}

		queries := []Query{{SQL: sql, Arguments: []any{args}}, {SQL: "SELECT $1::int8 + 1, $2::text, $1::int8", Arguments: []any{int64(41), "it's"}}}
		batch, err := conn.SendBatch(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		check("SendBatch", batch.QueryRow())
		check("SendBatch, positional", batch.QueryRow())
		if err := batch.Close(); err != nil {
			t.Fatal(err)
		}
		pipeline, err := conn.SendPipeline(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		check("SendPipeline", pipeline.QueryRow())
		check("SendPipeline, positional", pipeline.QueryRow())
		if err := pipeline.Close(); err != nil {
			t.Fatal(err)
		}
		if queries[0].SQL != sql {
			t.Fatalf("the caller's batch was changed: %q", queries[0].SQL)
		}

		if _, err := conn.Exec(ctx, "SELECT @missing", StrictNamedArgs{}); !errors.Is(err, ErrNamedArgs) {
			t.Fatalf("strict Exec = %v, want ErrNamedArgs", err)
		}
		if _, err := conn.SendBatch(ctx, []Query{{SQL: "SELECT @missing", Arguments: []any{StrictNamedArgs{}}}}); !errors.Is(err, ErrNamedArgs) {
			t.Fatalf("strict SendBatch = %v, want ErrNamedArgs", err)
		}
		check("after a refused rewrite", conn.QueryRow(ctx, sql, args))
	}
}
