// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "github.com/gsoultan/caduceus/wire"

// TraceConnectStartData is a connection about to be made.
type TraceConnectStartData struct {
	Config *wire.Config
}
