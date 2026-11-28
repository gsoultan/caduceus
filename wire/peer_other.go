// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build !linux

package wire

import (
	"fmt"
	"net"
)

// checkPeer cannot learn who the server runs as here, so requirepeer refuses
// every server rather than let one through unchecked, as libpq does where it
// has no way to ask.
func checkPeer(net.Conn, string) error {
	return fmt.Errorf("%w: peer credentials are not available on this system", ErrRequirePeer)
}
