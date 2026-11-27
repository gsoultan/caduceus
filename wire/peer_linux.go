// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build linux

package wire

import (
	"net"
	"os/user"
	"strconv"
	"syscall"
)

// peerUser is the name of the user the process at the other end of a Unix
// socket runs as, from its peer credentials.
func peerUser(conn net.Conn) (string, error) {
	raw := rawSocket(conn)
	if raw == nil {
		return "", ErrRequirePeer
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return "", err
	}
	if credErr != nil {
		return "", credErr
	}
	found, err := user.LookupId(strconv.FormatUint(uint64(cred.Uid), 10))
	if err != nil {
		return "", err
	}
	return found.Username, nil
}
