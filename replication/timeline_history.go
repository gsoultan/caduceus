// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// TimelineHistory is a timeline history file, as TIMELINE_HISTORY sends it:
// its name and its content.
type TimelineHistory struct {
	FileName string
	Content  []byte
}
