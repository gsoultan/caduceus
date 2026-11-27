// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "fmt"

// versionRefusal is a startup packet's version refused outright, by
// something in front of the server that knows only 3.0. It is
// ErrProtocolVersion and the server's error both, and tells Connect that
// asking for 3.0 may succeed where an offer of 3.0 in a
// NegotiateProtocolVersion would have needed no second dial.
type versionRefusal struct {
	asked   ProtocolVersion
	failure *Error
}

func (r *versionRefusal) Error() string {
	return fmt.Sprintf("%s: the server refused protocol %s at startup, and 3.0 is not allowed by min_protocol_version: %s",
		ErrProtocolVersion, r.asked, r.failure)
}

func (r *versionRefusal) Unwrap() []error { return []error{ErrProtocolVersion, r.failure} }
