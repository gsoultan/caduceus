// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// pacedConn is a transport that hands the reader what a server sent in reads
// of a size the test chooses, however much room the reader offers. A
// PostgreSQL server sends in buffers of 8 KiB, and how a result is cut into
// reads is what decides the size of the buffer it is read through.
type pacedConn struct {
	net.Conn
	stream []byte
	pace   int
}

func (c *pacedConn) Read(p []byte) (int, error) {
	if len(c.stream) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.stream[:min(c.pace, len(c.stream))])
	c.stream = c.stream[n:]
	return n, nil
}

// send queues what the server sends next.
func (c *pacedConn) send(messages ...[]byte) {
	for _, message := range messages {
		c.stream = append(c.stream, message...)
	}
}

// dataRows is count DataRow messages of size bytes each, header included.
func dataRows(count, size int) []byte {
	// The type, the length, the column count and the column's length.
	const overhead = 1 + 4 + 2 + 4
	row := make([]byte, size)
	row[0] = MsgDataRow
	binary.BigEndian.PutUint32(row[1:], uint32(size-1))
	binary.BigEndian.PutUint16(row[5:], 1)
	binary.BigEndian.PutUint32(row[7:], uint32(size-overhead))
	rows := make([]byte, 0, count*size)
	for range count {
		rows = append(rows, row...)
	}
	return rows
}

// readyForQuery is the message that ends an exchange.
func readyForQuery() []byte {
	return []byte{MsgReadyForQuery, 0, 0, 0, 5, TxIdle}
}

// readToReady reads an exchange to its end and returns how many times the
// read buffer was replaced on the way, by a larger one or a smaller.
func readToReady(t *testing.T, conn *Conn) (replaced int) {
	t.Helper()
	buffer := &conn.rbuf[0]
	for {
		kind, _, err := conn.Receive()
		if err != nil {
			t.Fatalf("Receive() = %v", err)
		}
		if now := &conn.rbuf[0]; now != buffer {
			buffer = now
			replaced++
		}
		if kind == MsgReadyForQuery {
			return replaced
		}
	}
}
