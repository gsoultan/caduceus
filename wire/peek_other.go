// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package wire

// peek cannot look at the socket here, and says nothing is waiting.
func (c *Conn) peek() (bool, error) { return false, nil }
