// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// SlotState is what READ_REPLICATION_SLOT says of a slot, PostgreSQL 15 and
// later: its type, physical or logical, and the WAL position and timeline it
// holds from. Exists is false for a slot that does not exist.
type SlotState struct {
	Exists          bool
	Type            string
	RestartLSN      LSN
	RestartTimeline int32
}
