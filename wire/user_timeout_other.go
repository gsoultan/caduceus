// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build !linux

package wire

import (
	"syscall"
	"time"
)

// tcpUserTimeout sets nothing where TCP_USER_TIMEOUT is not to be had; libpq
// ignores tcp_user_timeout there too.
func tcpUserTimeout(time.Duration) func(network, address string, raw syscall.RawConn) error {
	return nil
}
