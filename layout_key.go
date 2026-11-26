// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "reflect"

// layoutKey finds a structLayout in the cache: a struct type, a mapping, and a
// hash of the column names with their count. Two sets of names with one hash
// share a key, and the layout's own names tell them apart. By position only
// the count matters, and the hash is zero.
type layoutKey struct {
	t       reflect.Type
	how     mapping
	columns int
	hash    uint64
}
