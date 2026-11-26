// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// CopyFromTracer is told when each CopyFrom starts and ends.
type CopyFromTracer interface {
	TraceCopyFromStart(ctx context.Context, conn *Conn, data TraceCopyFromStartData) context.Context
	TraceCopyFromEnd(ctx context.Context, conn *Conn, data TraceCopyFromEndData)
}
