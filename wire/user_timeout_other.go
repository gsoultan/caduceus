// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build !linux

package wire

import (
	"syscall"
	"time"
)

// userTimeout does nothing: TCP_USER_TIMEOUT is Linux's alone, and libpq
// ignores tcp_user_timeout where it is not to be had, as this does.
func userTimeout(time.Duration) func(network, address string, raw syscall.RawConn) error {
	return nil
}
