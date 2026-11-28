// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"syscall"
	"time"
)

// tcpUserTimeoutOption is TCP_USER_TIMEOUT, which package syscall does not
// name.
const tcpUserTimeoutOption = 0x12

// tcpUserTimeout is the dialer's Control function that sets TCP_USER_TIMEOUT
// on a TCP socket, or nil when there is none to set.
func tcpUserTimeout(timeout time.Duration) func(network, address string, raw syscall.RawConn) error {
	if timeout <= 0 {
		return nil
	}
	millis := int(timeout.Milliseconds())
	return func(network, _ string, raw syscall.RawConn) error {
		if network == "unix" {
			return nil
		}
		var setErr error
		err := raw.Control(func(fd uintptr) {
			setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeoutOption, millis)
		})
		if err != nil {
			return err
		}
		return setErr
	}
}
