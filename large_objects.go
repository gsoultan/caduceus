// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// LargeObjectMode is how a large object is opened.
type LargeObjectMode int32

// The modes of lo_open, which may be combined.
const (
	LargeObjectModeWrite LargeObjectMode = 0x20000
	LargeObjectModeRead  LargeObjectMode = 0x40000
)

// LargeObjects works with PostgreSQL's large objects, values of up to 4 TB
// kept apart from any table and read and written in pieces. The server lets a
// large object be used only inside a transaction, so they are reached through
// a Tx, and a LargeObject opened in one is closed when it ends.
type LargeObjects struct {
	tx *Tx
}

// LargeObjects returns the transaction's large objects.
func (t *Tx) LargeObjects() LargeObjects { return LargeObjects{tx: t} }

// Create makes an empty large object and returns its OID. With oid zero the
// server chooses one.
func (lo LargeObjects) Create(ctx context.Context, oid uint32) (uint32, error) {
	var created uint32
	err := lo.tx.QueryRow(ctx, "SELECT lo_create($1)", oid).Scan(&created)
	return created, err
}

// Open opens the large object oid. Its reads and writes run in ctx.
func (lo LargeObjects) Open(ctx context.Context, oid uint32, mode LargeObjectMode) (*LargeObject, error) {
	var fd int32
	if err := lo.tx.QueryRow(ctx, "SELECT lo_open($1, $2)", oid, int32(mode)).Scan(&fd); err != nil {
		return nil, err
	}
	return &LargeObject{ctx: ctx, tx: lo.tx, fd: fd}, nil
}

// Unlink deletes the large object oid.
func (lo LargeObjects) Unlink(ctx context.Context, oid uint32) error {
	var done int32
	return lo.tx.QueryRow(ctx, "SELECT lo_unlink($1)", oid).Scan(&done)
}
