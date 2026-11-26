// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"hash/maphash"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/gsoultan/caduceus/wire"
)

// maxLayouts bounds the struct layouts kept, and maxLayoutBytes the bytes of
// column names they hold between them. A program has a handful of struct
// types and queries, so neither is reached by one that is not handed column
// names it does not choose; one that is, sees the cache emptied each time it
// fills, and the layouts derived afresh.
const (
	maxLayouts     = 1024
	maxLayoutBytes = 1 << 20
)

// layoutCache keeps the structLayout of each struct type, mapping and set of
// column names, so the RowToStruct functions derive one once rather than for
// every row. It is safe for concurrent use.
//
// Only a layout that maps is kept: a set of columns that does not fit the
// struct is refused again each time, so it costs nothing to keep. A kept
// layout by name has no more columns than its struct has fields, and one by
// position keeps no names at all. The cache holds at most about maxLayouts
// layouts and maxLayoutBytes of their names, a layout whose names alone pass
// that is never kept, and goroutines adding at the same moment may pass the
// bounds by one layout each before the cache is emptied.
type layoutCache struct {
	layouts sync.Map // layoutKey to *structLayout
	size    atomic.Int64
	bytes   atomic.Int64
	seed    maphash.Seed
}

// layouts is the cache the RowToStruct functions share.
var layouts = newLayoutCache()

func newLayoutCache() *layoutCache {
	return &layoutCache{seed: maphash.MakeSeed()}
}

// layout returns the layout of t for columns, from the cache or derived.
func (c *layoutCache) layout(t reflect.Type, how mapping, columns []wire.Field) *structLayout {
	key := c.key(t, how, columns)
	if found, ok := c.layouts.Load(key); ok && found.(*structLayout).describes(columns) {
		return found.(*structLayout)
	}
	layout := newStructLayout(t, how, columns)
	c.store(key, layout)
	return layout
}

// store keeps layout, emptying the cache when it is full. A layout that does
// not map, or whose names alone pass maxLayoutBytes, is not kept, nor is one
// whose key is already taken, by a collision; each is derived each time.
func (c *layoutCache) store(key layoutKey, layout *structLayout) {
	if !keepable(layout) {
		return
	}
	if _, loaded := c.layouts.LoadOrStore(key, layout); loaded {
		return
	}
	entries, bytes := c.size.Add(1), c.bytes.Add(layout.size())
	if entries > maxLayouts || bytes > maxLayoutBytes {
		c.layouts.Clear()
		c.size.Store(0)
		c.bytes.Store(0)
	}
}

// keepable reports whether a layout is worth keeping.
func keepable(layout *structLayout) bool {
	return layout.err == nil && layout.size() <= maxLayoutBytes
}

// key returns the key of the layout of t for columns. By position, the names
// do not matter, and are not hashed.
func (c *layoutCache) key(t reflect.Type, how mapping, columns []wire.Field) layoutKey {
	if how == byPos {
		return layoutKey{t: t, how: how, columns: len(columns)}
	}
	var h maphash.Hash
	h.SetSeed(c.seed)
	for i := range columns {
		h.WriteString(columns[i].Name)
		h.WriteByte(0)
	}
	return layoutKey{t: t, how: how, columns: len(columns), hash: h.Sum64()}
}

// len returns the number of layouts kept.
func (c *layoutCache) len() int { return int(c.size.Load()) }

// retained returns the bytes of column names the kept layouts hold.
func (c *layoutCache) retained() int64 { return c.bytes.Load() }
