// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// PrepareTracer is told when each Prepare starts and ends.
type PrepareTracer interface {
	TracePrepareStart(ctx context.Context, conn *Conn, data TracePrepareStartData) context.Context
	TracePrepareEnd(ctx context.Context, conn *Conn, data TracePrepareEndData)
}
