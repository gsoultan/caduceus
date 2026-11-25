// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build linux

package wire

import (
	"sync"
	"syscall"
)

// setLowWater sets SO_RCVLOWAT. A failure leaves the socket as it was, which
// is always safe, so it is not reported.
func setLowWater(raw syscall.RawConn, bytes int) {
	_ = raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVLOWAT, bytes)
	})
}

// lowWaterWakes reports whether lowering the mark makes the kernel report the
// bytes already waiting, which it does from Linux 4.18. Before that a reader
// parked behind a raised mark would sleep through the end of a result.
var lowWaterWakes = sync.OnceValue(func() bool {
	var name syscall.Utsname
	if err := syscall.Uname(&name); err != nil {
		return false
	}
	release := make([]byte, 0, len(name.Release))
	for _, c := range name.Release {
		if c == 0 {
			break
		}
		release = append(release, byte(c))
	}
	return kernelAtLeast(string(release), 4, 18)
})
