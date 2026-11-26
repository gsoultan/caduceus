// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// What CopyTo writes is what the server sent, in the COPY's format, and
// CopyFromReader sends it back as it is: a table copied out and into another
// comes out the same, in text, CSV and binary.
func TestLiveCopyToAndFromReader(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	from, to := liveTable(t, conn), liveTable(t, conn)
	if _, err := conn.Exec(ctx, "INSERT INTO "+from+" SELECT i, 'note '||i FROM generate_series(1, 20000) i"); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"", " (FORMAT csv)", " (FORMAT binary)"} {
		var out bytes.Buffer
		tag, err := conn.CopyTo(ctx, &out, "COPY "+from+" TO STDOUT"+format)
		if err != nil || tag.RowsAffected() != 20000 {
			t.Fatalf("CopyTo%s = %q, %v", format, tag, err)
		}
		if _, err := conn.Exec(ctx, "TRUNCATE "+to); err != nil {
			t.Fatal(err)
		}
		tag, err = conn.CopyFromReader(ctx, bytes.NewReader(out.Bytes()), "COPY "+to+" FROM STDIN"+format)
		if err != nil || tag.RowsAffected() != 20000 {
			t.Fatalf("CopyFromReader%s = %q, %v", format, tag, err)
		}
		var differ int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM (SELECT * FROM "+from+" EXCEPT SELECT * FROM "+to+") d").Scan(&differ); err != nil || differ != 0 {
			t.Fatalf("%s: %d rows differ, %v", format, differ, err)
		}
	}
}

// failingReader yields some data and then fails.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// failingWriter refuses every write.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// A reader that fails ends the COPY with CopyFail, so none of what it gave
// is copied, and its error is returned with the connection still usable. A
// writer that fails closes the connection rather than read the rest of the
// copy to drop it. A COPY in the wrong direction, a COPY the server refuses
// and one inside a transaction all leave the connection as they should.
func TestLiveCopyRawFailures(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	failure := errors.New("reader failed")

	_, err := conn.CopyFromReader(ctx, &failingReader{data: []byte("1\ta\n2\tb\n"), err: failure}, "COPY "+table+" FROM STDIN")
	if !errors.Is(err, failure) {
		t.Fatalf("CopyFromReader = %v, want the reader's error", err)
	}
	if got := countRows(t, conn, table); got != 0 {
		t.Fatalf("%d rows copied from a reader that failed", got)
	}

	if _, err := conn.CopyFromReader(ctx, strings.NewReader(""), "COPY "+table+" TO STDOUT"); !errors.Is(err, errCopyDirection) {
		t.Fatalf("CopyFromReader of a COPY TO = %v", err)
	}
	if _, err := conn.CopyTo(ctx, io.Discard, "COPY "+table+" FROM STDIN"); !errors.Is(err, errCopyDirection) {
		t.Fatalf("CopyTo of a COPY FROM = %v", err)
	}
	var server interface{ SQLState() string }
	if _, err := conn.CopyTo(ctx, io.Discard, "COPY no_such_table TO STDOUT"); !errors.As(err, &server) || server.SQLState() != "42P01" {
		t.Fatalf("CopyTo of a missing table = %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.CopyFromReader(ctx, strings.NewReader("7\tseven\n"), "COPY "+table+" FROM STDIN"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := tx.CopyTo(ctx, &out, "COPY "+table+" TO STDOUT"); err != nil || out.String() != "7\tseven\n" {
		t.Fatalf("CopyTo in the transaction = %q, %v", out.String(), err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, conn, table); got != 0 {
		t.Fatalf("%d rows after the transaction's rollback", got)
	}

	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" SELECT i FROM generate_series(1, 100000) i"); err != nil {
		t.Fatal(err)
	}
	// Another connection, so the table's cleanup still has one to drop it on.
	copier := liveConn(t, Options{})
	refused := errors.New("writer failed")
	if _, err := copier.CopyTo(ctx, failingWriter{refused}, "COPY "+table+" TO STDOUT"); !errors.Is(err, refused) {
		t.Fatalf("CopyTo = %v, want the writer's error", err)
	}
	if !copier.IsClosed() {
		t.Fatal("a writer that failed left the connection open mid-copy")
	}
}
