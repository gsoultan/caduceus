// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"fmt"
)

// The validators target_session_attrs sets as Config.ValidateConnect, which
// can be set there directly too. A server of PostgreSQL 14 or later reports
// in_hot_standby and default_transaction_read_only as it logs in, and is
// judged by those without a round trip; an older one is asked.

// ValidateReadWrite accepts a server whose sessions can write by default:
// target_session_attrs=read-write.
func ValidateReadWrite(ctx context.Context, conn *Conn) error {
	return expect(ctx, conn, "read-write", readOnly, false)
}

// ValidateReadOnly accepts a server whose sessions are read-only by default,
// a standby or one with default_transaction_read_only on:
// target_session_attrs=read-only.
func ValidateReadOnly(ctx context.Context, conn *Conn) error {
	return expect(ctx, conn, "read-only", readOnly, true)
}

// ValidatePrimary accepts a server that is not in recovery:
// target_session_attrs=primary.
func ValidatePrimary(ctx context.Context, conn *Conn) error {
	return expect(ctx, conn, "primary", inRecovery, false)
}

// ValidateStandby accepts a server in recovery: target_session_attrs=standby.
func ValidateStandby(ctx context.Context, conn *Conn) error {
	return expect(ctx, conn, "standby", inRecovery, true)
}

// ValidatePreferStandby accepts a standby, and a primary as a last resort,
// with ErrNotPreferred: target_session_attrs=prefer-standby.
func ValidatePreferStandby(ctx context.Context, conn *Conn) error {
	standby, err := inRecovery(ctx, conn)
	switch {
	case err != nil:
		return err
	case !standby:
		return ErrNotPreferred
	}
	return nil
}

// expect checks that what probe finds of conn is want, and says which kind
// was asked for if not.
func expect(ctx context.Context, conn *Conn, kind string, probe func(context.Context, *Conn) (bool, error), want bool) error {
	got, err := probe(ctx, conn)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%w: it is not %s", ErrSessionAttrs, kind)
	}
	return nil
}

// readOnly reports whether a session on conn is read-only by default: the
// server is a standby, or default_transaction_read_only is on.
func readOnly(ctx context.Context, conn *Conn) (bool, error) {
	standby, reported := conn.params["in_hot_standby"]
	readOnly, both := conn.params["default_transaction_read_only"]
	if reported && both {
		return standby == "on" || readOnly == "on", nil
	}
	return asked(ctx, conn, "SHOW transaction_read_only", "on")
}

// inRecovery reports whether the server is a standby.
func inRecovery(ctx context.Context, conn *Conn) (bool, error) {
	if standby, reported := conn.params["in_hot_standby"]; reported {
		return standby == "on", nil
	}
	return asked(ctx, conn, "SELECT pg_is_in_recovery()", "t")
}

// asked runs sql, whose one row has one column, and reports whether its value
// is yes.
func asked(ctx context.Context, conn *Conn, sql, yes string) (bool, error) {
	result, err := conn.QueryText(ctx, sql)
	if err != nil {
		return false, err
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		return false, ErrProtocol
	}
	return string(result.Rows[0][0]) == yes, nil
}
