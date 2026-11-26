// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// TraceQueryEndData is how a statement ended.
type TraceQueryEndData struct {
	CommandTag CommandTag
	Err        error
}
