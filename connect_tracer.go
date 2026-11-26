// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// ConnectTracer is told when Connect and ConnectConfig start and end.
type ConnectTracer interface {
	TraceConnectStart(ctx context.Context, data TraceConnectStartData) context.Context
	TraceConnectEnd(ctx context.Context, data TraceConnectEndData)
}
