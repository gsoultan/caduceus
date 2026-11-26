// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// BatchTracer is told when each SendBatch and SendPipeline starts, and when
// its results are closed, which is when it has ended.
type BatchTracer interface {
	TraceBatchStart(ctx context.Context, conn *Conn, data TraceBatchStartData) context.Context
	TraceBatchEnd(ctx context.Context, conn *Conn, data TraceBatchEndData)
}
