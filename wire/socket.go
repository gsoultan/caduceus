// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"crypto/tls"
	"net"
	"syscall"
)

// rawSocket returns the socket under conn, through TLS if it is wrapped in
// it, or nil for a connection that is no socket of the system's.
func rawSocket(conn net.Conn) syscall.RawConn {
	if wrapped, ok := conn.(*tls.Conn); ok {
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
