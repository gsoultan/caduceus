// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "slices"

// tracers holds Options.Tracer as each kind of tracer it is, nil for each it
// is not, so a statement checks a field rather than asserting a type.
type tracers struct {
	query   QueryTracer
	batch   BatchTracer
	copy    CopyFromTracer
	prepare PrepareTracer
}

// newTracers sorts tracer into the kinds it is.
func newTracers(tracer QueryTracer) tracers {
	t := tracers{query: tracer}
	t.batch, _ = tracer.(BatchTracer)
	t.copy, _ = tracer.(CopyFromTracer)
	t.prepare, _ = tracer.(PrepareTracer)
	return t
}

// queryStart is what a QueryTracer is told of a statement, with a copy of its
// arguments. Handing it the caller's slice would make that slice escape on
// every call, traced or not: QueryRow then allocated its variadic arguments,
// one allocation more for every statement.
func queryStart(sql string, args []any) TraceQueryStartData {
	return TraceQueryStartData{SQL: sql, Args: slices.Clone(args)}
}
