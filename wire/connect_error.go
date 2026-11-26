// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"strconv"
)

// ConnectError is how Connect fails: where it was connecting to, and why.
// It names the host, port, user and database, never the password, and
// unwraps to the cause, such as the server's *Error or ErrProtocolVersion.
type ConnectError struct {
	Host     string
	Port     uint16
	User     string
	Database string
	Err      error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("caduceus/wire: connecting to host=%s port=%s user=%s database=%s: %v",
		e.Host, strconv.Itoa(int(e.Port)), e.User, e.Database, e.Err)
}

func (e *ConnectError) Unwrap() error { return e.Err }

// connectError wraps err, if any, in a ConnectError for config.
func connectError(config *Config, err error) error {
	if err == nil {
		return nil
	}
	return &ConnectError{Host: config.Host, Port: config.Port, User: config.User, Database: config.Database, Err: err}
}
