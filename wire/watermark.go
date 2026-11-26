// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"crypto/tls"
	"net"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// watermarkAfter is how much of one result must have arrived before the
	// socket's low-water mark is raised. A long result reaches the client a
	// server buffer at a time, 8 KiB from PostgreSQL, and a reader that keeps
	// up is woken for each: a read that finds nothing, a park, a wake from the
	// poller, a read. Shorter results are left alone, since what the mark
	// saves is a share of the time a result takes and what it can cost is
	// fixed.
	watermarkAfter = 4 << 20
	// watermarkBytes is the raised mark: the kernel wakes the reader once this
	// much is waiting, an eighth as often, and no longer makes the server wake
	// it with every buffer it sends.
	watermarkBytes = 64 << 10
	// watermarkWait is how long after the reader last took bytes the mark is
	// lowered again. It is about what the end of a long result waits, with the
	// timer's own lateness on top, which a loaded machine adds to, and it
	// has to outlast the time watermarkBytes take to arrive, which it does for
	// any stream faster than 32 MB a second. A slower one has its mark lowered
	// once and left alone.
	watermarkWait = 2 * time.Millisecond
)

// watermark raises a socket's receive low-water mark while a long result
// streams in, so the reader wakes per 64 KiB and not per server buffer.
//
// The end of a result is shorter than the mark and would never wake the
// reader, so a raised mark always has a timer behind it, which lowers it; the
// kernel then reports whatever is waiting. The timer runs on a goroutine of
// its own and touches the socket option and one atomic, never the stream.
// Every raise is followed by arming the timer and the timer lowers without
// condition, so whichever order the two goroutines run in, the mark ends
// lowered: the direction that cannot stall.
type watermark struct {
	raw    syscall.RawConn
	probed bool
	// received is what the exchange has read so far. spent is an exchange
	// whose stream paused with the mark raised; it is not raised twice.
	received int
	spent    bool
	// raised is the reader's view of the mark and up the socket's, which the
	// timer clears.
	raised bool
	up     atomic.Bool
	timer  *time.Timer
}

// took accounts for n bytes read, and raises the mark or keeps it raised once
// the exchange is long enough.
func (c *Conn) took(n int) {
	w := &c.mark
	if w.spent || !w.past(n) {
		return
	}
	w.hold(c)
}

// past counts n more bytes of the exchange and reports whether it has run as
// far as watermarkAfter. Counted only that far, so no result is long enough
// to run the count past what an int holds.
func (w *watermark) past(n int) bool {
	w.received = min(w.received+n, watermarkAfter)
	return w.received == watermarkAfter
}

func (w *watermark) hold(c *Conn) {
	if w.raised && w.up.Load() {
		w.timer.Reset(watermarkWait)
		return
	}
	if w.raised {
		// The timer lowered it: the stream paused, or is too slow to gain.
		w.raised, w.spent = false, true
		return
	}
	raw := w.socket(c)
	if raw == nil {
		w.spent = true
		return
	}
	w.raised = true
	w.up.Store(true)
	setLowWater(raw, watermarkBytes)
	if w.timer == nil {
		w.timer = time.AfterFunc(watermarkWait, w.lower)
		return
	}
	w.timer.Reset(watermarkWait)
}

// lower puts the mark back to one byte. It is the timer's callback, and the
// reader's at the end of an exchange.
func (w *watermark) lower() {
	setLowWater(w.raw, 1)
	w.up.Store(false)
}

// settle ends an exchange: the mark comes down and the count starts again.
func (w *watermark) settle() {
	if w.raised {
		w.timer.Stop()
		w.lower()
		w.raised = false
	}
	w.received, w.spent = 0, false
}

// forgo leaves the mark alone for a stream that has no end to wait for, such
// as replication, where a message held back is latency and nothing else.
func (w *watermark) forgo() {
	w.settle()
	w.spent = true
}

// socket returns the TCP socket under the connection, or nil where the mark
// cannot be used: a Unix socket, which ignores it; a kernel that would not
// wake the reader when it is lowered; a configuration that turned it off.
func (w *watermark) socket(c *Conn) syscall.RawConn {
	if w.probed {
		return w.raw
	}
	w.probed = true
	if c.config.noWatermark() || !lowWaterWakes() {
		return nil
	}
	// The mark is for TCP: over a Unix socket a long result is a copy in
	// memory, with nothing to coalesce.
	conn := c.netConn
	if wrapped, ok := conn.(*tls.Conn); ok {
		conn = wrapped.NetConn()
	}
	if _, ok := conn.(*net.TCPConn); ok {
		w.raw = rawSocket(conn)
	}
	return w.raw
}
