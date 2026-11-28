// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Backup is what BASE_BACKUP reports of a backup taken: where in the WAL
// it begins and ends, which is what restoring it needs, and the tablespaces
// it holds.
type Backup struct {
	Start         LSN
	StartTimeline int32
	End           LSN
	EndTimeline   int32
	Tablespaces   []Tablespace
}
