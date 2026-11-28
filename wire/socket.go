// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"net"
	"syscall"
)

// rawSocket returns the socket under conn, through TLS or GSSAPI encryption
// if it is wrapped in one, or nil for a connection that is no socket of the
// system's.
func rawSocket(conn net.Conn) syscall.RawConn {
	if wrapped, ok := conn.(interface{ NetConn() net.Conn }); ok {
		conn = wrapped.NetConn()
	}
	socket, ok := conn.(syscall.Conn)
	if !ok {
		return nil
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return nil
	}
	return raw
}
