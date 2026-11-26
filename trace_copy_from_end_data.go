// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// TraceCopyFromEndData is how a CopyFrom ended.
type TraceCopyFromEndData struct {
	Rows int64
	Err  error
}
