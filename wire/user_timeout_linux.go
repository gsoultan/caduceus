// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build linux

package wire

import (
	"syscall"
	"time"
)

// tcpUserTimeout is TCP_USER_TIMEOUT, which the syscall package does not name.
const tcpUserTimeout = 0x12

// userTimeout sets TCP_USER_TIMEOUT on a TCP socket before it connects: how
// long sent data may go unacknowledged before the kernel drops the connection.
func userTimeout(timeout time.Duration) func(network, address string, raw syscall.RawConn) error {
	return func(network, _ string, raw syscall.RawConn) error {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil
		}
		var setErr error
		err := raw.Control(func(fd uintptr) {
			setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeout, int(timeout.Milliseconds()))
		})
		if err != nil {
			return err
		}
		return setErr
	}
}
