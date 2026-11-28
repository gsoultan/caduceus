// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// BaseBackupOptions are BASE_BACKUP's options. The zero value takes a
// backup the way the server does by default: a spread checkpoint, no WAL,
// waiting for the WAL the backup needs to be archived, with checksums
// verified and without a manifest, which pg_basebackup asks for and the
// server does not send unless asked.
type BaseBackupOptions struct {
	// Label names the backup in its backup_label. Empty is the server's
	// "base backup".
	Label string
	// FastCheckpoint starts with an immediate checkpoint rather than a
	// spread one, as pg_basebackup's --checkpoint=fast.
	FastCheckpoint bool
	// WAL includes, in the main archive, the WAL the backup needs to be
	// consistent, as pg_basebackup's --wal-method=fetch.
	WAL bool
	// NoWait does not wait for that WAL to be archived.
	NoWait bool
	// MaxRate limits the rate the server sends at, in kilobytes a second;
	// zero is no limit, and a limit is 32 to 1048576.
	MaxRate int
	// TablespaceMap includes tablespace_map, as pg_basebackup's tar format
	// does, for the symbolic links of pg_tblspc.
	TablespaceMap bool
	// NoVerifyChecksums does not verify the checksums of the pages sent,
	// where the cluster has them.
	NoVerifyChecksums bool
	// Manifest is yes, no (the server's default), or force-encode, which writes
	// every file name of the manifest hex-encoded. ManifestChecksums is the
	// manifest's checksum algorithm: NONE, CRC32C (the default), SHA224,
	// SHA256, SHA384 or SHA512.
	Manifest          string
	ManifestChecksums string
	// Target is where the server puts the backup: client (the default),
	// sending it on the connection; server, writing it to TargetDetail, a
	// directory of the server's, which needs pg_write_server_files; or
	// blackhole, discarding it. Only client calls the open function.
	Target       string
	TargetDetail string
	// Compression has the server compress each archive, with gzip, lz4 or
	// zstd; CompressionDetail is its detail, as level=5 or workers=4.
	Compression       string
	CompressionDetail string
	// Progress, if set, asks the server for progress and is called with
	// the bytes of the current tablespace sent so far. It also has the
	// server estimate each tablespace's size first, which costs a pass over
	// the files.
	Progress func(done int64)
}
