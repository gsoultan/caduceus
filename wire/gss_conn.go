// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	// gssPacketSize is the largest packet, length and token, that
	// PostgreSQL reads from a GSSAPI-encrypted connection, and
	// gssPlaintextSize what the client puts in one: what is left once a
	// wrap token's header, confounder and checksum are allowed for.
	gssPacketSize    = 16384
	gssPlaintextSize = gssPacketSize - 4 - 256
	// gssMaxPacket bounds a packet from the server.
	gssMaxPacket = 1 << 20
)

// gssConn is a connection encrypted with GSSAPI, as gssencmode makes it:
// every packet a four-byte length and a token the wrapper wraps.
type gssConn struct {
	net.Conn
	wrapper GSSWrapper
	// pending is plaintext of a packet read and not yet returned.
	pending []byte
	packet  []byte
}

// NetConn is the connection under the encryption, as tls.Conn's is, for
// the socket options and the liveness check that look at the socket.
func (c *gssConn) NetConn() net.Conn { return c.Conn }

func (c *gssConn) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		if err := c.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// next reads a packet and unwraps it.
func (c *gssConn) next() error {
	var length [4]byte
	if _, err := io.ReadFull(c.Conn, length[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 || size > gssMaxPacket {
		return fmt.Errorf("%w: a GSSAPI packet of %d bytes", ErrProtocol, size)
	}
	c.packet = append(c.packet[:0], make([]byte, size)...)
	if _, err := io.ReadFull(c.Conn, c.packet); err != nil {
		return err
	}
	plain, err := c.wrapper.Unwrap(c.packet)
	if err != nil {
		return fmt.Errorf("caduceus/wire: GSSAPI encryption: %w", err)
	}
	c.pending = plain
	return nil
}

// Write wraps p in packets the server can read, and writes them at once.
func (c *gssConn) Write(p []byte) (int, error) {
	var out []byte
	for rest := p; len(rest) > 0; {
		chunk := rest[:min(len(rest), gssPlaintextSize)]
		rest = rest[len(chunk):]
		token, err := c.wrapper.Wrap(chunk)
		if err != nil {
			return 0, fmt.Errorf("caduceus/wire: GSSAPI encryption: %w", err)
		}
		if len(token) > gssPacketSize-4 {
			return 0, fmt.Errorf("caduceus/wire: GSSAPI encryption: a token of %d bytes is more than the server reads", len(token))
		}
		out = binary.BigEndian.AppendUint32(out, uint32(len(token)))
		out = append(out, token...)
	}
	if _, err := c.Conn.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
