// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"fmt"
	"slices"
)

// QueryRewriter rewrites a statement and its arguments before it is sent. A
// statement whose only argument is a QueryRewriter is rewritten by it, as
// NamedArgs rewrites @name placeholders into $1, $2 and the arguments to
// match. Exec, Query, QueryRow, QueryInto, SendBatch and SendPipeline all
// honour it, on a Conn and on a Tx.
type QueryRewriter interface {
	RewriteQuery(ctx context.Context, conn *Conn, sql string, args []any) (newSQL string, newArgs []any, err error)
}

// rewritten applies a QueryRewriter given as a statement's only argument.
func (s *Conn) rewritten(ctx context.Context, sql string, args []any) (string, []any, error) {
	if len(args) != 1 {
		return sql, args, nil
	}
	rewriter, ok := args[0].(QueryRewriter)
	if !ok {
		return sql, args, nil
	}
	return s.rewrite(ctx, rewriter, sql)
}

// rewrite calls rewriter with an argument slice of its own. Handing it the
// caller's would make that slice escape on every call, a rewriter or not:
// QueryRow then allocated its variadic arguments, one allocation more for
// every statement.
func (s *Conn) rewrite(ctx context.Context, rewriter QueryRewriter, sql string) (string, []any, error) {
	return rewriter.RewriteQuery(ctx, s, sql, []any{rewriter})
}

// rewrittenAll applies rewritten to each query of a batch, in a copy of the
// slice so the caller's is left as it was, and only if any needs it.
func (s *Conn) rewrittenAll(ctx context.Context, queries []Query) ([]Query, error) {
	if !slices.ContainsFunc(queries, rewrites) {
		return queries, nil
	}
	out := slices.Clone(queries)
	for i := range out {
		sql, args, err := s.rewritten(ctx, out[i].SQL, out[i].Arguments)
		if err != nil {
			return nil, fmt.Errorf("query %d: %w", i, err)
		}
		out[i] = Query{SQL: sql, Arguments: args}
	}
	return out, nil
}

// rewrites reports whether a query's only argument is a QueryRewriter.
func rewrites(q Query) bool {
	if len(q.Arguments) != 1 {
		return false
	}
	_, ok := q.Arguments[0].(QueryRewriter)
	return ok
}
