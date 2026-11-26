// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// NamedArgs binds arguments by name: a statement run with a NamedArgs as its
// only argument has each @name in it, outside string literals, quoted
// identifiers and comments, replaced by a positional parameter bound to the
// value of that name. A name used twice is one parameter. A name the map
// lacks binds NULL, as in pgx; StrictNamedArgs refuses it instead.
//
// A name starts with a letter or an underscore and goes on with letters,
// digits and underscores, so the operators @>, <@, @@ and @- are left alone.
// The values are bound as parameters, never interpolated.
type NamedArgs map[string]any

var _ QueryRewriter = NamedArgs(nil)

// RewriteQuery implements QueryRewriter.
func (na NamedArgs) RewriteQuery(_ context.Context, _ *Conn, sql string, _ []any) (string, []any, error) {
	return rewriteNamed(sql, na, false)
}
