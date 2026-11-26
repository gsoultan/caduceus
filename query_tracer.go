// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// QueryTracer is told when each statement Exec, Query, QueryRow and QueryInto
// run starts and ends, on a Conn and on a Tx, for logging, metrics or tracing
// spans. Set it as Options.Tracer; it may also be a BatchTracer, a
// CopyFromTracer, a PrepareTracer and a ConnectTracer, which are then told of
// those too.
//
// The context TraceQueryStart returns is the one the statement runs in and
// TraceQueryEnd gets, so a span can be carried from one to the other. A
// query's end is when its rows are read to the end or closed, so a tracer sees
// how long the caller held them. Both run on the goroutine using the
// connection and must not use it.
type QueryTracer interface {
	TraceQueryStart(ctx context.Context, conn *Conn, data TraceQueryStartData) context.Context
	TraceQueryEnd(ctx context.Context, conn *Conn, data TraceQueryEndData)
}
