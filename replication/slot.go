// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Slot is a replication slot CreateReplicationSlot created: its name, the
// position from which it is consistent, the snapshot it exported if it did,
// and its plugin.
type Slot struct {
	Name            string
	ConsistentPoint LSN
	SnapshotName    string
	Plugin          string
}
