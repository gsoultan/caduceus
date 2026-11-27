// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"net"
)

// checkPeer refuses a Unix-socket server not running as RequirePeer, before
// anything, a password least of all, is sent to it. Over TCP there is no peer
// to ask, and libpq checks nothing there either.
func checkPeer(config *Config, conn net.Conn) error {
	if config.RequirePeer == "" || !config.unix() {
		return nil
	}
	name, err := peerUser(conn)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequirePeer, err)
	}
	if name != config.RequirePeer {
		return fmt.Errorf("%w: it runs as %q, not %q", ErrRequirePeer, name, config.RequirePeer)
	}
	return nil
}
