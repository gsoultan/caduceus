// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"strconv"
)

// ProtocolVersion is a version of PostgreSQL's frontend/backend protocol, the
// major version in the high 16 bits and the minor in the low, as the startup
// packet carries it.
type ProtocolVersion uint32

const (
	// Protocol30 is the protocol every server since PostgreSQL 7.4 speaks.
	Protocol30 ProtocolVersion = 3 << 16
	// Protocol32 is PostgreSQL 18's: the same messages, with a cancel key of
	// up to 256 bytes where 3.0's is 4, so a query cannot be cancelled by
	// whoever guesses 32 bits. 3.1 was never used by any server.
	Protocol32 ProtocolVersion = 3<<16 | 2
)

// maxCancelKey is the longest cancel key protocol 3.2 allows.
const maxCancelKey = 256

// refusesProtocol reports whether an error answering a startup packet that
// asked for more than 3.0 can be a refusal of the version: 08P01, as
// PgBouncer before 1.24 says "bad packet header", or 0A000.
func refusesProtocol(asked ProtocolVersion, failure *Error) bool {
	return asked > Protocol30 && (failure.Code == "08P01" || failure.Code == "0A000")
}

func (v ProtocolVersion) String() string {
	return strconv.Itoa(int(v>>16)) + "." + strconv.Itoa(int(v&0xffff))
}

// parseProtocolVersion reads min_protocol_version or max_protocol_version as
// libpq 18 does: "3.0", "3.2", or "latest", which is the newest this driver
// speaks.
func parseProtocolVersion(key, text string) (ProtocolVersion, error) {
	switch text {
	case "":
		return 0, nil
	case "3.0":
		return Protocol30, nil
	case "3.2", "latest":
		return Protocol32, nil
	}
	return 0, fmt.Errorf("%w: %s=%s is not 3.0, 3.2 or latest", ErrInvalidConnString, key, text)
}
