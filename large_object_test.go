// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"bytes"
	"io"
	"testing"
)

// A large object written in pieces larger than one statement's chunk reads
// back the same, seeks and truncates as io.Seeker and a file would, ends in
// io.EOF, closes twice without failing the transaction, and is gone once
// unlinked.
func TestLiveLargeObjects(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	objects := tx.LargeObjects()
	oid, err := objects.Create(ctx, 0)
	if err != nil || oid == 0 {
		t.Fatalf("Create = %d, %v", oid, err)
	}
	object, err := objects.Open(ctx, oid, LargeObjectModeRead|LargeObjectModeWrite)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("0123456789abcdef"), (3*largeObjectChunk)/16+5)
	if n, err := object.Write(data); err != nil || n != len(data) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if position, err := object.Tell(); err != nil || position != int64(len(data)) {
		t.Fatalf("Tell = %d, %v", position, err)
	}
	if position, err := object.Seek(0, io.SeekStart); err != nil || position != 0 {
		t.Fatalf("Seek = %d, %v", position, err)
	}
	read, err := io.ReadAll(object)
	if err != nil || !bytes.Equal(read, data) {
		t.Fatalf("ReadAll = %d bytes, %v; want %d", len(read), err, len(data))
	}
	if n, err := object.Read(make([]byte, 10)); n != 0 || err != io.EOF {
		t.Fatalf("Read at the end = %d, %v; want io.EOF", n, err)
	}
	if position, err := object.Seek(-6, io.SeekEnd); err != nil || position != int64(len(data)-6) {
		t.Fatalf("Seek from the end = %d, %v", position, err)
	}
	tail := make([]byte, 6)
	if n, err := io.ReadFull(object, tail); err != nil || n != 6 || string(tail) != string(data[len(data)-6:]) {
		t.Fatalf("the tail = %q, %v", tail[:n], err)
	}
	if err := object.Truncate(10); err != nil {
		t.Fatal(err)
	}
	if position, err := object.Seek(0, io.SeekEnd); err != nil || position != 10 {
		t.Fatalf("size after Truncate = %d, %v", position, err)
	}
	if err := object.Close(); err != nil {
		t.Fatal(err)
	}
	if err := object.Close(); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
	if err := objects.Unlink(ctx, oid); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Open(ctx, oid, LargeObjectModeRead); err == nil {
		t.Fatal("an unlinked object opened")
	}
}
