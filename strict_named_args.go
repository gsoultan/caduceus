// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// StrictNamedArgs is NamedArgs that refuses a statement naming an argument
// the map lacks, and a map holding an argument the statement does not name,
// either of which is more often a typo than a wish to bind NULL.
type StrictNamedArgs map[string]any

var _ QueryRewriter = StrictNamedArgs(nil)

// RewriteQuery implements QueryRewriter.
func (sa StrictNamedArgs) RewriteQuery(_ context.Context, _ *Conn, sql string, _ []any) (string, []any, error) {
	return rewriteNamed(sql, sa, true)
}
