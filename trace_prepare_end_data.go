// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// TracePrepareEndData is how a Prepare ended. AlreadyPrepared is one that
// found the statement held already and sent nothing.
type TracePrepareEndData struct {
	AlreadyPrepared bool
	Err             error
}
