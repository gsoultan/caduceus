// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// A wait that ended with its context used to leave, once in a few hundred, a
// read deadline in the past on the connection. The context moves the deadline
// there from a goroutine of its own, and stopping that does not wait for one
// already started: it ran after the wait had cleared the deadline. The next
// exchange under a context that cannot end then timed out at once, and its
// replies were read by the exchange after it as its own.
func TestWaitLeavesNoReadDeadlineBehind(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	late := newLateConn(client)
	conn := standIn(late, &Config{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	if _, err := conn.WaitForNotification(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForNotification() = %v, want the context's deadline", err)
	}

	select {
	case <-late.moved:
	case <-time.After(10 * lateDeadline):
		// The wait stopped the context's goroutine before it started.
	}
	if deadline := late.readDeadline(); !deadline.IsZero() {
		t.Fatalf("the connection was left with a read deadline of %v, want none", deadline)
	}
}

// The same wait ended by a cancellation, which has no deadline of its own to
// race: the read wakes only because the deadline was moved.
func TestWaitEndedByCancellationLeavesNoReadDeadlineBehind(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	late := newLateConn(client)
	conn := standIn(late, &Config{})

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(5*time.Millisecond, cancel)
	if _, err := conn.WaitForNotification(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForNotification() = %v, want the cancellation", err)
	}
	if deadline := late.readDeadline(); !deadline.IsZero() {
		t.Fatalf("the connection was left with a read deadline of %v, want none", deadline)
	}
	if conn.IsClosed() {
		t.Fatal("the wait closed the connection")
	}
}
