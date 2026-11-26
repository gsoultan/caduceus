// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"fmt"
	"time"
)

// cancelTimeout bounds the CancelRequest sent when a context ends mid-query.
const cancelTimeout = 5 * time.Second

// Guard ties one exchange on a connection to a context. Start it with Watch
// before the first write and end it with Done after the last read.
//
// A context with neither a deadline nor a Done channel — context.Background —
// costs nothing: no timer, no goroutine, no allocation.
type Guard struct {
	conn     *Conn
	ctx      context.Context
	stop     func() bool
	deadline bool
}

// Watch starts guarding an exchange with ctx. If ctx ends while a read or write
// is blocked, that call returns a timeout, which Done turns into ctx's error.
func (c *Conn) Watch(ctx context.Context) Guard {
	g := Guard{conn: c, ctx: ctx}
	c.overdue, c.wrote = false, false
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.netConn.SetDeadline(deadline)
		g.deadline = true
	}
	if ctx.Done() != nil {
		netConn := c.netConn
		g.stop = context.AfterFunc(ctx, func() { _ = netConn.SetDeadline(time.Unix(1, 0)) })
	}
	return g
}

// Done ends the exchange and reports how it went, given the error the exchange
// itself returned.
//
// When the context ended during the exchange, the server is asked to cancel
// whatever it is running and the connection is closed: where its stream stands
// is no longer known, and a pool must not hand it to anyone. The returned error
// then wraps the context's, so errors.Is(err, context.DeadlineExceeded) holds.
//
// The connection is closed the same way when a read or a write of the exchange
// ran into a deadline that was not the context's: one left on the socket by
// whoever used it before. A timeout leaves a connection intact between
// exchanges, never in the middle of one, where what was sent is still to be
// answered and the next exchange would read those replies as its own. The
// error is then returned as it is, for the context has not ended.
//
// An error that only happens to be a timeout, with the connection's own reads
// and writes all on time, is the exchange's business and is returned as it is.
//
// An error of an exchange that wrote nothing is marked for SafeToRetry.
func (g Guard) Done(err error) error {
	if err = g.done(err); err != nil && !g.conn.wrote {
		return NotSent(err)
	}
	return err
}

// done is Done without marking the error.
func (g Guard) done(err error) error {
	fired := g.stop != nil && !g.stop()
	if !fired && !g.conn.overdue {
		if g.deadline && !g.conn.closed.Load() {
			_ = g.conn.netConn.SetDeadline(time.Time{})
		}
		return err
	}
	g.conn.abort()
	// The socket notices a deadline before the context says it has passed.
	awaitExpiry(g.ctx)
	cause := context.Cause(g.ctx)
	if cause == nil || err == nil {
		// The context is alive, so the deadline was not its own. Or the
		// exchange finished as the context ended: its result stands, though
		// the connection's deadline is already in the past.
		return err
	}
	return fmt.Errorf("caduceus: %w (%w)", cause, err)
}

// abort asks the server to cancel the running query and closes the connection.
// The cancel request travels on a connection of its own, from a goroutine, so
// that a slow server does not make the caller wait past its own deadline.
func (c *Conn) abort() {
	if c.closed.Load() {
		return
	}
	_ = c.fail(ErrClosed)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
		defer cancel()
		_ = c.CancelRequest(ctx)
	}()
}
