// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package wire

import (
	"errors"
	"io"
	"syscall"
)

// peek reports whether the server has sent anything the connection has not
// read, without reading it or waiting for it, and io.EOF once it has hung up.
func (c *Conn) peek() (bool, error) {
	raw := rawSocket(c.netConn)
	if raw == nil {
		return false, nil
	}
	var n int
	var recvErr error
	var probe [1]byte
	err := raw.Read(func(fd uintptr) bool {
		n, _, recvErr = syscall.Recvfrom(int(fd), probe[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		return true
	})
	switch {
	case err != nil:
		return false, err
	case errors.Is(recvErr, syscall.EAGAIN), errors.Is(recvErr, syscall.EWOULDBLOCK):
		return false, nil
	case recvErr != nil:
		return false, recvErr
	case n == 0:
		return false, io.EOF
	}
	return true, nil
}
