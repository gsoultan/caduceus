// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// SlotOptions are how CreateReplicationSlot creates a slot.
//
// Temporary is a slot dropped when the connection ends. Physical is a slot
// for physical replication, with no plugin; ReserveWAL has it hold WAL from
// now rather than from when it is first used. Snapshot is what a logical
// slot does with the snapshot it starts from: "" or "export", exported for
// pg_export_snapshot until the next command on the connection; "noexport",
// none; "use", used by the transaction the connection is in. TwoPhase decodes
// prepared transactions at PREPARE, PostgreSQL 14 and later.
type SlotOptions struct {
	Temporary  bool
	Physical   bool
	ReserveWAL bool
	Snapshot   string
	TwoPhase   bool
}
