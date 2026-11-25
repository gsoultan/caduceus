// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build !linux

package wire

import "syscall"

// setLowWater does nothing where the mark is not used.
func setLowWater(syscall.RawConn, int) {}

// lowWaterWakes is false off Linux: what the poller does with a low-water
// mark differs by system, and only Linux has been measured.
func lowWaterWakes() bool { return false }
