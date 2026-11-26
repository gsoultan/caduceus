// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// cancelRequests listens where a connection's cancel requests go, and reports
// the process each one names.
func cancelRequests(t *testing.T) (*Config, <-chan uint32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	seen := make(chan uint32, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			request := make([]byte, 12)
			if _, err := io.ReadFull(conn, request); err == nil && binary.BigEndian.Uint32(request[4:]) == cancelRequestCode {
				seen <- binary.BigEndian.Uint32(request[8:])
			}
			_ = conn.Close()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return &Config{Host: address.IP.String(), Port: uint16(address.Port)}, seen
}

// An exchange under a context that cannot end sets no deadline, so nothing
// stood ready for one that timed out all the same, on a deadline left on the
// socket: the error was handed back and the connection stayed open, with the
// statement sent and its replies unread. The next exchange read them as its
// own, and so did every one after it.
func TestExchangeEndedByADeadlineNotItsOwnClosesTheConnection(t *testing.T) {
	config, cancelled := cancelRequests(t)
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	// A server that takes the statement and has not answered yet.
	go func() { _, _ = io.Copy(io.Discard, server) }()
	conn := standIn(client, config)
	conn.pid = 4321
	_ = client.SetReadDeadline(time.Unix(1, 0))

	_, err := conn.Exec(context.Background(), "SELECT 1")
	if !IsTimeout(err) {
		t.Fatalf("Exec() = %v, want the timeout", err)
	}
	if !conn.IsClosed() {
		t.Fatal("the connection was left open with the statement's replies unread")
	}
	select {
	case pid := <-cancelled:
		if pid != 4321 {
			t.Fatalf("cancel request for process %d, want 4321", pid)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server was not asked to cancel the statement")
	}
}

// A timeout between exchanges is the wait for a notification ending, and must
// not count against the exchange that follows.
func TestTimeoutBeforeAnExchangeIsNotHeldAgainstIt(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	conn := standIn(client, &Config{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	if _, err := conn.WaitForNotification(ctx); err == nil {
		t.Fatal("WaitForNotification() = nil on a silent connection")
	}

	go func() {
		// Take the statement, then answer it: an empty query's reply and
		// ReadyForQuery.
		buf := make([]byte, 64)
		_, _ = server.Read(buf)
		_, _ = server.Write([]byte{MsgEmptyQueryResponse, 0, 0, 0, 4, MsgReadyForQuery, 0, 0, 0, 5, TxIdle})
	}()
	if _, err := conn.Exec(context.Background(), ""); err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	if conn.IsClosed() {
		t.Fatal("the exchange after a wait closed the connection")
	}
}

// The same leftover deadline under a context that can end but has not: the
// exchange was closed, rightly, and reported as the context's deadline having
// passed, which it had not. A caller that tells its own cancellation from a
// failure by the error was told the wrong one.
func TestTimeoutThatIsNotTheContextsIsNotReportedAsIts(t *testing.T) {
	config, _ := cancelRequests(t)
	for _, guard := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
	}{
		{"cancellable", func() (context.Context, context.CancelFunc) { return context.WithCancel(t.Context()) }},
		{"a deadline far off", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(t.Context(), time.Hour)
		}},
	} {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		go func() { _, _ = io.Copy(io.Discard, server) }()
		conn := standIn(client, config)
		ctx, cancel := guard.context()
		defer cancel()

		watching := conn.Watch(ctx)
		// Set once the guard has put the context's own deadline, if any, on
		// the socket: the one left behind by something else.
		_ = client.SetReadDeadline(time.Unix(1, 0))
		_, err := conn.exec("SELECT 1")
		err = watching.Done(err)

		if !IsTimeout(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("%s: got %v, want the timeout and nothing about a context that is still alive", guard.name, err)
		}
		if ctx.Err() != nil {
			t.Fatalf("%s: the context ended: %v", guard.name, ctx.Err())
		}
		if !conn.IsClosed() {
			t.Fatalf("%s: the connection was left open with the statement's replies unread", guard.name)
		}
	}
}

// timedOut is an error that is a timeout and came from somewhere else than
// the connection: a copy's source reading from a network of its own.
type timedOut struct{}

func (timedOut) Error() string   { return "the source timed out" }
func (timedOut) Timeout() bool   { return true }
func (timedOut) Temporary() bool { return true }

// An exchange that ended in order with an error that happens to be a timeout
// was taken for one the connection's deadline had cut off: the connection was
// closed and the error dressed as the context's deadline, though neither the
// connection nor the context had anything to do with it.
func TestTimeoutFromElsewhereLeavesTheConnectionAlone(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	conn := standIn(client, &Config{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()

	// Nothing was written, so it is marked safe to retry, and otherwise as it
	// was.
	err := conn.Watch(ctx).Done(timedOut{})
	if !errors.Is(err, timedOut{}) || err.Error() != "the source timed out" {
		t.Fatalf("Done() = %v, want the error as it was", err)
	}
	if conn.IsClosed() {
		t.Fatal("the connection was closed for an error that was not its own")
	}
}

// What a context's deadline does to an exchange must not change: closed, and
// reported as the context's.
func TestDeadlineOfTheContextEndsTheExchangeAsTheContexts(t *testing.T) {
	config, cancelled := cancelRequests(t)
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	go func() { _, _ = io.Copy(io.Discard, server) }()
	conn := standIn(client, config)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	_, err := conn.Exec(ctx, "SELECT 1")
	if !errors.Is(err, context.DeadlineExceeded) || !IsTimeout(err) {
		t.Fatalf("Exec() = %v, want the context's deadline wrapping the timeout", err)
	}
	if !conn.IsClosed() {
		t.Fatal("the connection was left open")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the server was not asked to cancel the statement")
	}
}
