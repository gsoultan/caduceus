// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build !linux

package wire

import (
	"fmt"
	"net"
)

// peerUser is not to be had here: the peer credentials of a Unix socket are
// read with SO_PEERCRED, which is Linux's.
func peerUser(net.Conn) (string, error) {
	return "", fmt.Errorf("%w: requirepeer is supported on Linux only", ErrRequirePeer)
}
