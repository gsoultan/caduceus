// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"net"
	"time"
)

// interruption is a context's hold on a connection's reads: when the context
// ends, the read deadline is moved into the past, which is how a read already
// blocked is told to return.
type interruption struct {
	stop func() bool
	done chan struct{}
}

// interrupt has the reads on netConn end when ctx does, until the hold is
// released. A context that cannot end holds nothing.
func interrupt(ctx context.Context, netConn net.Conn) interruption {
	if ctx.Done() == nil {
		return interruption{}
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = netConn.SetReadDeadline(time.Unix(1, 0))
	})
	return interruption{stop: stop, done: done}
}

// release ends the hold, and returns once the deadline can no longer be moved.
//
// The context moves it from a goroutine of its own, and stopping that does not
// wait for one already started. A caller that cleared the deadline straight
// after stopping could be overtaken: the deadline went into the past after it
// was cleared, and stayed there for whoever read from the connection next.
func (i interruption) release() {
	if i.stop != nil && !i.stop() {
		<-i.done
	}
}
