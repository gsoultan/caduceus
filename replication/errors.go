// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "errors"

// ErrMalformed is a replication message shorter or stranger than the protocol
// allows. A stream that produces one should be ended: a decoder that guessed
// would emit wrong data.
var ErrMalformed = errors.New("caduceus/replication: malformed replication message")

// ErrLSNFormat is text that is not an LSN in the server's "X/Y" notation.
var ErrLSNFormat = errors.New("caduceus/replication: LSN must be two hexadecimal halves separated by '/'")
