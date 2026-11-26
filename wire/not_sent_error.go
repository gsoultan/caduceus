// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "errors"

// notSentError is an error of an exchange that wrote nothing to the server,
// so whatever it was to run did not run. See SafeToRetry.
type notSentError struct {
	err error
}

func (e *notSentError) Error() string     { return e.err.Error() }
func (e *notSentError) Unwrap() error     { return e.err }
func (e *notSentError) SafeToRetry() bool { return true }

// NotSent marks err as the failure of something that sent nothing to the
// server, for SafeToRetry: a connection found busy or closed before a
// statement was written, say. A nil err stays nil.
func NotSent(err error) error {
	if err == nil || SafeToRetry(err) {
		return err
	}
	return &notSentError{err: err}
}

// SafeToRetry reports whether err is from something that cannot have reached
// the server: nothing of it was written, so it did not run, and running it
// again, on this connection if it is still open or on another, cannot run it
// twice. Every exchange through a Guard is marked so when it wrote nothing,
// as when its context was over before it started, or an argument could not be
// encoded. An error that is not marked may or may not have run.
func SafeToRetry(err error) bool {
	var retry interface{ SafeToRetry() bool }
	return errors.As(err, &retry) && retry.SafeToRetry()
}
