// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"net"
	"os/user"
	"strconv"
	"syscall"
)

// checkPeer refuses a server on a Unix socket whose process does not run as
// want, which the kernel reports as the socket's peer credentials.
func checkPeer(conn net.Conn, want string) error {
	raw := rawSocket(conn)
	if raw == nil {
		return fmt.Errorf("%w: the connection is no socket", ErrRequirePeer)
	}
	var credentials *syscall.Ucred
	var credErr error
	err := raw.Control(func(fd uintptr) {
		credentials, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err == nil {
		err = credErr
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRequirePeer, err)
	}
	peer, err := user.LookupId(strconv.FormatUint(uint64(credentials.Uid), 10))
	if err != nil {
		return fmt.Errorf("%w: user %d: %w", ErrRequirePeer, credentials.Uid, err)
	}
	if peer.Username != want {
		return fmt.Errorf("%w: requirepeer is %q and the server runs as %q", ErrRequirePeer, want, peer.Username)
	}
	return nil
}
