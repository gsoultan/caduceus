// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

// Package caduceus is a PostgreSQL driver written from the protocol up, in pure
// Go, with no dependencies: not database/sql, not pgx, not pglogrepl.
//
//	conn, err := caduceus.Connect(ctx, "postgres://app:secret@db:5432/app", caduceus.Options{})
//	if err != nil {
//		return err
//	}
//	defer conn.Close(ctx)
//
//	var name string
//	err = conn.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", 42).Scan(&name)
//
//	tx, err := conn.Begin(ctx)
//	if err != nil {
//		return err
//	}
//	defer tx.Rollback(ctx)
//	if _, err := tx.Exec(ctx, "UPDATE users SET name = $1 WHERE id = $2", "Ada", 42); err != nil {
//		return err
//	}
//	return tx.Commit(ctx)
//
// A Conn is one connection and belongs to one goroutine at a time. A Pool is
// safe for concurrent use, and hands back the same Rows, Row and Tx.
//
// The subpackages are usable alone: wire speaks the protocol, codec converts
// between Go values and PostgreSQL's formats, and replication decodes logical
// replication's pgoutput stream.
package caduceus
