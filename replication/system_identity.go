// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// SystemIdentity is what IDENTIFY_SYSTEM says of the server: the cluster's
// system identifier, its current timeline, the WAL position it has flushed
// to, and the database the connection is to, empty for a physical one.
type SystemIdentity struct {
	SystemID string
	Timeline int32
	XLogPos  LSN
	Database string
}
