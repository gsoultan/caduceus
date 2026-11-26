// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

type traceKey struct{}

// recorder is every kind of tracer, and writes down what it is told. Each end
// checks that it got the context its start returned, which is how a span is
// carried from one to the other.
type recorder struct {
	t      *testing.T
	events []string
	starts int
}

func (r *recorder) start(ctx context.Context, event string) context.Context {
	r.starts++
	r.events = append(r.events, event)
	return context.WithValue(ctx, traceKey{}, r.starts)
}

func (r *recorder) end(ctx context.Context, event string) {
	if ctx.Value(traceKey{}) == nil {
		r.t.Errorf("%s: the end did not get the context the start returned", event)
	}
	r.events = append(r.events, event)
}

func (r *recorder) TraceQueryStart(ctx context.Context, _ *Conn, data TraceQueryStartData) context.Context {
	return r.start(ctx, "query "+data.SQL+" "+strconv.Itoa(len(data.Args)))
}

func (r *recorder) TraceQueryEnd(ctx context.Context, _ *Conn, data TraceQueryEndData) {
	r.end(ctx, "end "+data.CommandTag.String()+" "+errorCode(data.Err))
}

func (r *recorder) TraceBatchStart(ctx context.Context, _ *Conn, data TraceBatchStartData) context.Context {
	return r.start(ctx, "batch "+strconv.Itoa(len(data.Queries)))
}

func (r *recorder) TraceBatchEnd(ctx context.Context, _ *Conn, data TraceBatchEndData) {
	r.end(ctx, "batch end "+errorCode(data.Err))
}

func (r *recorder) TraceCopyFromStart(ctx context.Context, _ *Conn, data TraceCopyFromStartData) context.Context {
	return r.start(ctx, "copy "+strings.Join(data.ColumnNames, ","))
}

func (r *recorder) TraceCopyFromEnd(ctx context.Context, _ *Conn, data TraceCopyFromEndData) {
	r.end(ctx, "copy end "+strconv.FormatInt(data.Rows, 10)+" "+errorCode(data.Err))
}

func (r *recorder) TracePrepareStart(ctx context.Context, _ *Conn, data TracePrepareStartData) context.Context {
	return r.start(ctx, "prepare "+data.Name)
}

func (r *recorder) TracePrepareEnd(ctx context.Context, _ *Conn, data TracePrepareEndData) {
	r.end(ctx, "prepare end "+strconv.FormatBool(data.AlreadyPrepared)+" "+errorCode(data.Err))
}

func (r *recorder) TraceConnectStart(ctx context.Context, data TraceConnectStartData) context.Context {
	return r.start(ctx, "connect "+strconv.FormatBool(data.Config != nil))
}

func (r *recorder) TraceConnectEnd(ctx context.Context, data TraceConnectEndData) {
	r.end(ctx, "connect end "+strconv.FormatBool(data.Conn != nil)+" "+errorCode(data.Err))
}

// take returns what was recorded since the last take.
func (r *recorder) take() string {
	events := strings.Join(r.events, "; ")
	r.events = nil
	return events
}

func errorCode(err error) string {
	var server *wire.Error
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &server):
		return server.Code
	}
	return "error"
}

func tracedConn(t *testing.T) (*Conn, *recorder) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	r := &recorder{t: t}
	conn, err := Connect(t.Context(), dsn, Options{Tracer: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if got := r.take(); got != "connect true; connect end true ok" {
		t.Fatalf("connect traced as %q", got)
	}
	return conn, r
}

// Every statement is traced from start to end, with what the caller gave and
// how it ended; and a query ends when its rows do, not when it is sent.
func TestLiveTracerSeesStatements(t *testing.T) {
	conn, r := tracedConn(t)
	ctx := t.Context()
	if _, err := conn.Exec(ctx, "SELECT $1::int", 1); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); got != "query SELECT $1::int 1; end SELECT 1 ok" {
		t.Errorf("Exec traced as %q", got)
	}
	if _, err := conn.Exec(ctx, "SELECT * FROM no_such_table_here"); err == nil {
		t.Fatal("no error")
	}
	if got := r.take(); got != "query SELECT * FROM no_such_table_here 0; end  42P01" {
		t.Errorf("a failed Exec traced as %q", got)
	}

	rows, err := conn.Query(ctx, "SELECT generate_series(1, 3)")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.take(); got != "query SELECT generate_series(1, 3) 0" {
		t.Errorf("an open query traced as %q, want it not ended yet", got)
	}
	for rows.Next() {
	}
	rows.Close()
	rows.Close()
	if got := r.take(); got != "end SELECT 3 ok" {
		t.Errorf("the query's rows ended traced as %q, want one end", got)
	}

	rows, err = conn.Query(ctx, "SELECT generate_series(1, 3)")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if got := r.take(); got != "query SELECT generate_series(1, 3) 0; end SELECT 3 ok" {
		t.Errorf("rows closed unread traced as %q", got)
	}

	var n int
	if err := conn.QueryRow(ctx, "SELECT 7").Scan(&n); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	if got := r.take(); got != "query SELECT 7 0; end SELECT 1 ok" {
		t.Errorf("QueryRow traced as %q", got)
	}
	// An error before there are rows ends the query there and then.
	if err := conn.QueryRow(ctx, "SELECT $1::int", "x").Scan(&n); err == nil {
		t.Fatal("no error")
	}
	if got := r.take(); got != "query SELECT $1::int 1; end  22P02" {
		t.Errorf("a refused QueryRow traced as %q", got)
	}
	if err := conn.QueryRow(ctx, "SELECT", 1, 2).Scan(); err == nil {
		t.Fatal("no error")
	}
	if got := r.take(); got != "query SELECT 2; end  error" {
		t.Errorf("a QueryRow refused before it was sent traced as %q", got)
	}

	if _, err := conn.Prepare(ctx, "seven", "SELECT 7"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Prepare(ctx, "seven", "SELECT 7"); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); got != "prepare seven; prepare end false ok; prepare seven; prepare end true ok" {
		t.Errorf("Prepare traced as %q", got)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); !strings.Contains(got, "query SELECT 1 0; end SELECT 1 ok") || !strings.Contains(got, "query COMMIT 0; end COMMIT ok") {
		t.Errorf("a transaction traced as %q", got)
	}
}

// A batch and a pipeline are traced from when they are sent to when their
// results are closed, once however often they are closed.
func TestLiveTracerSeesBatchesAndCopies(t *testing.T) {
	conn, r := tracedConn(t)
	ctx := t.Context()
	queries := []Query{{SQL: "SELECT 1"}, {SQL: "SELECT 2"}}
	results, err := conn.SendBatch(ctx, queries)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.take(); got != "batch 2" {
		t.Errorf("an open batch traced as %q", got)
	}
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}
	_ = results.Close()
	if got := r.take(); got != "batch end ok" {
		t.Errorf("a closed batch traced as %q", got)
	}

	pipeline, err := conn.SendPipeline(ctx, []Query{{SQL: "SELECT 1"}, {SQL: "SELECT 1/0"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = pipeline.Close()
	_ = pipeline.Close()
	if got := r.take(); got != "batch 2; batch end 22012" {
		t.Errorf("a pipeline traced as %q", got)
	}

	table := "caduceus_trace_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := conn.Exec(ctx, "CREATE TABLE "+QuoteIdentifier(table)+" (id int, note text)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS "+QuoteIdentifier(table)) })
	r.take()
	copied, err := conn.CopyFrom(ctx, []string{table}, []string{"id", "note"}, CopyFromRows([][]any{{1, "a"}, {2, "b"}}))
	if err != nil || copied != 2 {
		t.Fatal(copied, err)
	}
	if got := r.take(); got != "copy id,note; copy end 2 ok" {
		t.Errorf("CopyFrom traced as %q", got)
	}
}

// A failed connection is traced too, with the error and no connection.
func TestLiveTracerSeesAFailedConnect(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "caduceus_no_such_database"
	r := &recorder{t: t}
	if _, err := ConnectConfig(t.Context(), config, Options{Tracer: r}); err == nil {
		t.Fatal("no error")
	}
	if got := r.take(); got != "connect true; connect end false 3D000" {
		t.Errorf("a failed connect traced as %q", got)
	}
}

// A tracer must not cost a connection without one anything: QueryRow stays at
// one allocation.
func TestLiveNoTracerAllocatesNothing(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := context.Background()
	var n int64
	allocs := testing.AllocsPerRun(200, func() {
		if err := conn.QueryRow(ctx, "SELECT $1::int8", int64(7)).Scan(&n); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 1 {
		t.Fatalf("QueryRow allocates %v times, want at most 1", allocs)
	}
}

// A query is ended for its tracer once. Its trace used to stay on the
// connection after it ended, and the rows of a later batch that failed on the
// connection ended it a second time.
func TestLiveTracerEndsAQueryOnce(t *testing.T) {
	conn, r := tracedConn(t)
	rows, err := conn.Query(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	r.take()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	results, err := conn.SendBatch(ctx, []Query{{SQL: "SELECT pg_sleep(5)"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := results.Exec(); err == nil {
		t.Fatal("the batch outlived its context")
	}
	_ = results.Close()
	if got := r.take(); got != "batch 1; batch end error" {
		t.Errorf("a batch cut short traced as %q", got)
	}
}
