// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "fmt"

// ArgumentError reports an argument that could not be encoded for its
// parameter. Nothing was sent; the connection is unaffected.
type ArgumentError struct {
	Index int
	Err   error
}

func (e *ArgumentError) Error() string {
	return fmt.Sprintf("caduceus: argument $%d: %v", e.Index+1, e.Err)
}

func (e *ArgumentError) Unwrap() error { return e.Err }
