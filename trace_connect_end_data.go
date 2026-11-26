// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// TraceConnectEndData is how connecting ended.
type TraceConnectEndData struct {
	Conn *Conn
	Err  error
}
