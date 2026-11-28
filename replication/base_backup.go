// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gsoultan/caduceus/wire"
)

// ErrBaseBackupVersion is a server older than PostgreSQL 15, whose
// BASE_BACKUP sends its archives in a format of its own; 14 is out of
// support.
var ErrBaseBackupVersion = errors.New("caduceus/replication: BASE_BACKUP needs PostgreSQL 15 or later")

// BaseBackup takes a base backup with BASE_BACKUP, on a connection made with
// replication=true, by a role with the REPLICATION attribute. Each archive
// the server sends, a tar file for each tablespace and then the manifest,
// is written to what open returns for it, and closed once it is all
// written; a program writes them to files, or unpacks them with archive/tar.
//
// Restoring the backup needs the WAL from Backup.Start to Backup.End, which
// options.WAL includes, or which a physical slot or archive_command keeps.
//
// An error from open or a writer stops the backup: the server is asked to
// cancel it, what it had already sent is read and discarded, and the error
// is returned with the connection usable. ctx bounds all of it.
func BaseBackup(ctx context.Context, conn *wire.Conn, options BaseBackupOptions, open func(Archive) (io.WriteCloser, error)) (Backup, error) {
	if !backupProtocol(conn.ParameterStatus("server_version")) {
		return Backup{}, fmt.Errorf("%w: the server is %s", ErrBaseBackupVersion, conn.ParameterStatus("server_version"))
	}
	command, err := baseBackupCommand(options)
	if err != nil {
		return Backup{}, err
	}
	guard := conn.Watch(ctx)
	stream := &backupStream{ctx: ctx, conn: conn, options: options, open: open, result: -1}
	return stream.backup, guard.Done(stream.run(command))
}

// backupProtocol reports whether a server_version is 15 or later.
func backupProtocol(version string) bool {
	digits := version
	if i := strings.IndexFunc(version, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits = version[:i]
	}
	major, err := strconv.Atoi(digits)
	return err == nil && major >= 15
}

// baseBackupCommand writes BASE_BACKUP in the option list PostgreSQL 15
// introduced. Every value a caller gives goes in as a quoted literal.
func baseBackupCommand(options BaseBackupOptions) (string, error) {
	if options.MaxRate != 0 && (options.MaxRate < 32 || options.MaxRate > 1048576) {
		return "", fmt.Errorf("caduceus/replication: MaxRate %d is not 32 to 1048576 kB/s", options.MaxRate)
	}
	var list []string
	add := func(on bool, option string) {
		if on {
			list = append(list, option)
		}
	}
	quoted := func(name, value string) {
		add(value != "", name+" "+QuoteLiteral(value))
	}
	quoted("LABEL", options.Label)
	add(options.Progress != nil, "PROGRESS")
	add(options.FastCheckpoint, "CHECKPOINT 'fast'")
	add(options.WAL, "WAL")
	add(options.NoWait, "WAIT false")
	add(options.MaxRate != 0, "MAX_RATE "+strconv.Itoa(options.MaxRate))
	add(options.TablespaceMap, "TABLESPACE_MAP")
	add(options.NoVerifyChecksums, "VERIFY_CHECKSUMS false")
	quoted("MANIFEST", options.Manifest)
	quoted("MANIFEST_CHECKSUMS", options.ManifestChecksums)
	quoted("TARGET", options.Target)
	quoted("TARGET_DETAIL", options.TargetDetail)
	quoted("COMPRESSION", options.Compression)
	quoted("COMPRESSION_DETAIL", options.CompressionDetail)
	if len(list) == 0 {
		return "BASE_BACKUP", nil
	}
	return "BASE_BACKUP (" + strings.Join(list, ", ") + ")", nil
}
