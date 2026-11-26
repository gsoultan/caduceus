// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "errors"

// protocolRefusal is a server's error answering a startup packet that asked
// for a protocol newer than 3.0, before any authentication: what a server
// older than the NegotiateProtocolVersion message (0A000) or a pooler that
// knows only 3.0 (PgBouncer before 1.24, 08P01, "bad packet header") says
// where a current server would offer 3.0 instead.
type protocolRefusal struct {
	err *Error
}

func (r *protocolRefusal) Error() string { return r.err.Error() }
func (r *protocolRefusal) Unwrap() error { return r.err }

// refusesProtocol reports whether a startup error is one, and therefore
// whether asking again for 3.0 can succeed.
func refusesProtocol(asked ProtocolVersion, failure *Error) bool {
	return asked > Protocol30 && (failure.Code == "08P01" || failure.Code == "0A000")
}

// refusedProtocol reports whether err is a protocolRefusal.
func refusedProtocol(err error) bool {
	var refusal *protocolRefusal
	return errors.As(err, &refusal)
}
