// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// noticingConn connects with a small receive buffer to a table whose trigger
// raises a NOTICE for every row it takes, so a COPY that does not read while
// it writes fills both sockets.
func noticingConn(t *testing.T) (*Conn, string, *atomic.Int64) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	notices := new(atomic.Int64)
	config.OnNotice = func(*wire.Error) { notices.Add(1) }
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := new(net.Dialer).DialContext(ctx, network, address)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(64 << 10)
		}
		return conn, err
	}
	conn, err := ConnectConfig(t.Context(), config, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	table := liveTable(t, conn)
	name := "caduceus_notice_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := conn.Exec(t.Context(), "CREATE FUNCTION "+name+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE NOTICE 'row %: %', NEW.id, repeat('n', 1000); RETURN NEW; END $$; CREATE TRIGGER "+name+" BEFORE INSERT ON "+table+" FOR EACH ROW EXECUTE FUNCTION "+name+"()"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+name+"() CASCADE") })
	return conn, table, notices
}

// The rows are more than the server's socket and the client's can hold
// between them, 80 MB, and so are the notices, 20 MB, so whichever of the two
// stops reading the other stalls.
const noticedRows = 20_000

var noticedNote = strings.Repeat("x", 4096)

// A server that answers every row with a NOTICE writes as much as it reads.
// A COPY that only wrote filled the server's socket with notices, the server
// stopped reading to wait for it, and both sat blocked until the deadline.
func TestLiveCopyFromReadsNoticesWhileItWrites(t *testing.T) {
	conn, table, notices := noticingConn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	copied, err := conn.CopyFrom(ctx, Identifier{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromSlice(noticedRows, func(i int) ([]any, error) {
		return []any{i, noticedNote}, nil
	}))
	if err != nil || copied != noticedRows {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}
	if notices.Load() != noticedRows {
		t.Errorf("%d notices, want %d", notices.Load(), noticedRows)
	}
	if countRows(t, conn, table) != noticedRows {
		t.Error("rows missing")
	}
}

// The same for COPY data from a reader.
func TestLiveCopyFromReaderReadsNoticesWhileItWrites(t *testing.T) {
	conn, table, notices := noticingConn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	data := &noticedLines{}
	tag, err := conn.CopyFromReader(ctx, data, "COPY "+table+" (id, note) FROM STDIN")
	if err != nil || tag.RowsAffected() != noticedRows {
		t.Fatalf("CopyFromReader = %q, %v", tag, err)
	}
	if notices.Load() != noticedRows {
		t.Errorf("%d notices, want %d", notices.Load(), noticedRows)
	}
}

// noticedLines yields noticedRows lines of COPY text without holding them.
type noticedLines struct {
	next    int
	pending []byte
	// duplicate gives every line after the first the same key.
	duplicate bool
}

func (l *noticedLines) Read(p []byte) (int, error) {
	if len(l.pending) == 0 {
		if l.next == noticedRows {
			return 0, io.EOF
		}
		id := l.next
		if l.duplicate {
			id = min(id, 1)
		}
		l.pending = []byte(strconv.Itoa(id) + "\t" + noticedNote + "\n")
		l.next++
	}
	n := copy(p, l.pending)
	l.pending = l.pending[n:]
	return n, nil
}

// Inside a transaction the stream owes no reply until its first chunk goes
// out, so the replies are read only once it says it has sent one.
func TestLiveCopyFromInATransactionReadsNoticesWhileItWrites(t *testing.T) {
	conn, table, notices := noticingConn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	copied, err := tx.CopyFrom(ctx, Identifier{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromSlice(noticedRows, func(i int) ([]any, error) {
		return []any{i, noticedNote}, nil
	}))
	if err != nil || copied != noticedRows {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if notices.Load() != noticedRows {
		t.Errorf("%d notices, want %d", notices.Load(), noticedRows)
	}
}

// Once the server has refused a COPY, the rows still to come would be
// discarded, so they are not pulled from the source or sent: a duplicate key
// in the second row used to cost the whole 80 MB anyway.
func TestLiveCopyFromStopsOnceTheServerRefuses(t *testing.T) {
	conn := liveConn(t, Options{})
	table := liveTable(t, conn)
	pulled := 0
	_, err := conn.CopyFrom(t.Context(), Identifier{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromSlice(noticedRows, func(i int) ([]any, error) {
		pulled++
		return []any{min(i, 1), noticedNote}, nil
	}))
	if !isCode(err, "23505") {
		t.Fatalf("CopyFrom = %v, want 23505", err)
	}
	if pulled == noticedRows {
		t.Errorf("all %d rows were pulled after the server refused the second", pulled)
	}
	if countRows(t, conn, table) != 0 {
		t.Error("rows kept from a refused COPY")
	}
}

func isCode(err error, code string) bool {
	var server *wire.Error
	return errors.As(err, &server) && server.Code == code
}

// The same for data from a reader.
func TestLiveCopyFromReaderStopsOnceTheServerRefuses(t *testing.T) {
	conn := liveConn(t, Options{})
	table := liveTable(t, conn)
	data := &noticedLines{duplicate: true}
	_, err := conn.CopyFromReader(t.Context(), data, "COPY "+table+" (id, note) FROM STDIN")
	if !isCode(err, "23505") {
		t.Fatalf("CopyFromReader = %v, want 23505", err)
	}
	if data.next == noticedRows {
		t.Errorf("all %d lines were read after the server refused the second", data.next)
	}
}
