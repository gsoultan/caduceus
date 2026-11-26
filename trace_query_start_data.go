// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// TraceQueryStartData is a statement about to run, as the caller gave it,
// before any QueryRewriter in Args has rewritten it. Args is a copy of
// the caller's slice, which the tracer may keep.
type TraceQueryStartData struct {
	SQL  string
	Args []any
}
