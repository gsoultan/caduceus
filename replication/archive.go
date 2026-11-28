// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Archive is one archive of a base backup: a tar file, compressed where
// BaseBackupOptions.Compression is set, of a tablespace, or the backup's
// manifest.
type Archive struct {
	// Name is the archive's name, base.tar for the main data directory and
	// <oid>.tar for a tablespace, with .gz, .lz4 or .zst where compressed;
	// backup_manifest for the manifest.
	Name string
	// Tablespace is the directory of the tablespace it holds on the server,
	// and empty for the main data directory and for the manifest.
	Tablespace string
	// Manifest reports whether this is the backup manifest, a JSON file.
	Manifest bool
}
