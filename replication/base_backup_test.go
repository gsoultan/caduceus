// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/caduceus/wire"
)

func TestBaseBackupCommand(t *testing.T) {
	tests := []struct {
		name    string
		options BaseBackupOptions
		want    string
	}{
		{"the server's defaults", BaseBackupOptions{}, "BASE_BACKUP"},
		{"every option", BaseBackupOptions{
			Label: "nightly", Progress: func(int64) {}, FastCheckpoint: true, WAL: true, NoWait: true,
			MaxRate: 1024, TablespaceMap: true, NoVerifyChecksums: true, Manifest: "force-encode",
			ManifestChecksums: "SHA256", Target: "server", TargetDetail: "/backups/x",
			Compression: "zstd", CompressionDetail: "level=3",
		}, "BASE_BACKUP (LABEL 'nightly', PROGRESS, CHECKPOINT 'fast', WAL, WAIT false, MAX_RATE 1024, TABLESPACE_MAP, VERIFY_CHECKSUMS false, MANIFEST 'force-encode', MANIFEST_CHECKSUMS 'SHA256', TARGET 'server', TARGET_DETAIL '/backups/x', COMPRESSION 'zstd', COMPRESSION_DETAIL 'level=3')"},
		// A label is the caller's text: it is quoted, so it cannot end the
		// option list and add one of its own.
		{"a label is quoted", BaseBackupOptions{Label: "it's', TARGET 'server"}, "BASE_BACKUP (LABEL 'it''s'', TARGET ''server')"},
		{"the lowest rate", BaseBackupOptions{MaxRate: 32}, "BASE_BACKUP (MAX_RATE 32)"},
		{"the highest rate", BaseBackupOptions{MaxRate: 1048576}, "BASE_BACKUP (MAX_RATE 1048576)"},
	}
	for _, tt := range tests {
		got, err := baseBackupCommand(tt.options)
		if err != nil || got != tt.want {
			t.Errorf("%s: %q, %v, want %q", tt.name, got, err, tt.want)
		}
	}
	for _, rate := range []int{-1, 31, 1048577} {
		if _, err := baseBackupCommand(BaseBackupOptions{MaxRate: rate}); err == nil {
			t.Errorf("MaxRate %d was accepted", rate)
		}
	}
}

func TestBackupProtocol(t *testing.T) {
	for version, want := range map[string]bool{
		"15.0": true, "16.15 (Ubuntu 16.15-0ubuntu0.24.04.1)": true, "18beta1": true, "100": true,
		"14.12": false, "9.6.24": false, "": false, "beta": false,
	} {
		if got := backupProtocol(version); got != want {
			t.Errorf("backupProtocol(%q) = %v, want %v", version, got, want)
		}
	}
}

// scriptedServer is the server's end of a connection that has logged in as
// a server of version, and the cancel requests made to it.
type scriptedServer struct {
	net.Conn
	mu      sync.Mutex
	cancels int
}

func (s *scriptedServer) canceled() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancels
}

func scriptedConn(t *testing.T, version string) (*wire.Conn, *scriptedServer) {
	t.Helper()
	config, err := wire.ParseConfig("host=db user=u sslmode=disable replication=true")
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	scripted := &scriptedServer{Conn: server}
	dials := 0
	config.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		dials++
		if dials == 1 {
			return client, nil
		}
		// A cancel request comes on a connection of its own.
		cancelClient, cancelServer := net.Pipe()
		go func() {
			_, _ = io.ReadFull(cancelServer, make([]byte, 16))
			scripted.mu.Lock()
			scripted.cancels++
			scripted.mu.Unlock()
			_ = cancelServer.Close()
		}()
		return cancelClient, nil
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		var length [4]byte
		_, _ = io.ReadFull(server, length[:])
		_, _ = io.ReadFull(server, make([]byte, binary.BigEndian.Uint32(length[:])-4))
		login := pgMessage('R', []byte{0, 0, 0, 0})
		login = append(login, pgMessage('S', []byte("server_version\x00"+version+"\x00"))...)
		login = append(login, pgMessage('K', []byte{0, 0, 0, 1, 0, 0, 0, 2})...)
		_, _ = server.Write(append(login, pgMessage('Z', []byte{'I'})...))
	}()
	conn, err := wire.Connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	return conn, scripted
}

func pgMessage(kind byte, body []byte) []byte {
	out := []byte{kind, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(4+len(body)))
	return append(out, body...)
}

func rowDescription(names ...string) []byte {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(names)))
	for _, name := range names {
		body = append(body, name...)
		body = append(body, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 25, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0)
	}
	return pgMessage('T', body)
}

// dataRow is a row of text columns, a nil one NULL.
func dataRow(columns ...[]byte) []byte {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(columns)))
	for _, column := range columns {
		if column == nil {
			body = binary.BigEndian.AppendUint32(body, 0xffffffff)
			continue
		}
		body = binary.BigEndian.AppendUint32(body, uint32(len(column)))
		body = append(body, column...)
	}
	return pgMessage('D', body)
}

func copyData(kind byte, data ...string) []byte {
	return pgMessage('d', append([]byte{kind}, strings.Join(data, "")...))
}

func progressReport(done uint64) []byte {
	return pgMessage('d', binary.BigEndian.AppendUint64([]byte{'p'}, done))
}

// backupScript is a backup as PostgreSQL 15 and later send it, of the main
// data directory and one tablespace, with progress.
func backupScript(copy ...[]byte) []byte {
	var out []byte
	for _, part := range [][]byte{
		rowDescription("recptr", "tli"), dataRow([]byte("0/2000028"), []byte("1")), pgMessage('C', []byte("SELECT\x00")),
		rowDescription("spcoid", "spclocation", "size"),
		dataRow(nil, nil, []byte("30000")), dataRow([]byte("16384"), []byte("/srv/ts"), []byte("8")),
		pgMessage('C', []byte("SELECT\x00")),
		pgMessage('H', []byte{0, 0, 0}),
	} {
		out = append(out, part...)
	}
	for _, part := range copy {
		out = append(out, part...)
	}
	for _, part := range [][]byte{
		pgMessage('c', nil),
		rowDescription("recptr", "tli"), dataRow([]byte("0/2000100"), []byte("1")), pgMessage('C', []byte("SELECT\x00")),
		pgMessage('Z', []byte{'I'}),
	} {
		out = append(out, part...)
	}
	return out
}

// serve reads the client's query and answers it with script, and reports
// the query.
func serve(server net.Conn, script []byte) <-chan string {
	query := make(chan string, 1)
	go func() {
		var header [5]byte
		if _, err := io.ReadFull(server, header[:]); err != nil {
			close(query)
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(header[1:])-4)
		_, _ = io.ReadFull(server, body)
		query <- string(bytes.TrimRight(body, "\x00"))
		_, _ = server.Write(script)
	}()
	return query
}

// archiveRecorder keeps what each archive was written, and whether it was
// closed.
type archiveRecorder struct {
	archives []Archive
	contents []*bytes.Buffer
	closed   []bool
	fail     func(Archive) error
}

func (r *archiveRecorder) open(archive Archive) (io.WriteCloser, error) {
	if r.fail != nil {
		if err := r.fail(archive); err != nil {
			return nil, err
		}
	}
	r.archives = append(r.archives, archive)
	r.contents = append(r.contents, &bytes.Buffer{})
	r.closed = append(r.closed, false)
	return recordedArchive{r, len(r.archives) - 1}, nil
}

type recordedArchive struct {
	r *archiveRecorder
	i int
}

func (a recordedArchive) Write(p []byte) (int, error) { return a.r.contents[a.i].Write(p) }
func (a recordedArchive) Close() error                { a.r.closed[a.i] = true; return nil }

// Each archive of a backup is written to what open made for it and closed,
// the manifest last, and the positions and tablespaces are reported.
func TestBaseBackupStream(t *testing.T) {
	conn, server := scriptedConn(t, "17.2")
	query := serve(server, backupScript(
		copyData('n', "base.tar\x00", "\x00"), copyData('d', "main-"), progressReport(5), copyData('d', "data"), progressReport(9),
		copyData('n', "16384.tar\x00", "/srv/ts\x00"), copyData('d', "space"),
		copyData('m'), copyData('d', `{"PostgreSQL-Backup-Manifest-Version": 1}`),
	))
	recorder := &archiveRecorder{}
	var progress []int64
	backup, err := BaseBackup(t.Context(), conn, BaseBackupOptions{Label: "x", Progress: func(done int64) { progress = append(progress, done) }}, recorder.open)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-query; got != "BASE_BACKUP (LABEL 'x', PROGRESS)" {
		t.Errorf("query = %q", got)
	}
	want := Backup{Start: 0x2000028, StartTimeline: 1, End: 0x2000100, EndTimeline: 1, Tablespaces: []Tablespace{
		{Size: 30000 * 1024}, {OID: 16384, Location: "/srv/ts", Size: 8 * 1024},
	}}
	if !sameBackup(backup, want) {
		t.Errorf("backup = %+v, want %+v", backup, want)
	}
	wantArchives := []Archive{{Name: "base.tar"}, {Name: "16384.tar", Tablespace: "/srv/ts"}, {Name: "backup_manifest", Manifest: true}}
	wantContents := []string{"main-data", "space", `{"PostgreSQL-Backup-Manifest-Version": 1}`}
	if len(recorder.archives) != len(wantArchives) {
		t.Fatalf("archives = %+v", recorder.archives)
	}
	for i := range wantArchives {
		if recorder.archives[i] != wantArchives[i] || recorder.contents[i].String() != wantContents[i] || !recorder.closed[i] {
			t.Errorf("archive %d = %+v %q closed %v, want %+v %q", i, recorder.archives[i], recorder.contents[i], recorder.closed[i], wantArchives[i], wantContents[i])
		}
	}
	if len(progress) != 2 || progress[0] != 5 || progress[1] != 9 {
		t.Errorf("progress = %v", progress)
	}
}

func sameBackup(a, b Backup) bool {
	if a.Start != b.Start || a.StartTimeline != b.StartTimeline || a.End != b.End || a.EndTimeline != b.EndTimeline || len(a.Tablespaces) != len(b.Tablespaces) {
		return false
	}
	for i := range a.Tablespaces {
		if a.Tablespaces[i] != b.Tablespaces[i] {
			return false
		}
	}
	return true
}

// A backup without progress reports no size, rather than a size of zero.
func TestBaseBackupWithoutProgress(t *testing.T) {
	conn, server := scriptedConn(t, "15.0")
	script := bytes.Replace(backupScript(copyData('n', "base.tar\x00", "\x00")), dataRow(nil, nil, []byte("30000")), dataRow(nil, nil, nil), 1)
	serve(server, script)
	backup, err := BaseBackup(t.Context(), conn, BaseBackupOptions{}, (&archiveRecorder{}).open)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.Tablespaces) == 0 || backup.Tablespaces[0].Size != -1 {
		t.Errorf("tablespaces = %+v", backup.Tablespaces)
	}
}

// An error of the caller's stops the backup: the server is asked to cancel,
// the archive being written is closed, nothing more is written, and the
// error is the caller's, with the connection read through to its end.
func TestBaseBackupWriterFails(t *testing.T) {
	conn, server := scriptedConn(t, "16.1")
	full := errors.New("disk full")
	serve(server, backupScript(
		copyData('n', "base.tar\x00", "\x00"), copyData('d', "main"),
		copyData('n', "16384.tar\x00", "/srv/ts\x00"), copyData('d', "space"),
		copyData('m'), copyData('d', "{}"),
	))
	recorder := &archiveRecorder{fail: func(archive Archive) error {
		if archive.Tablespace != "" {
			return full
		}
		return nil
	}}
	_, err := BaseBackup(t.Context(), conn, BaseBackupOptions{}, recorder.open)
	if !errors.Is(err, full) {
		t.Fatalf("err = %v, want the writer's", err)
	}
	if len(recorder.archives) != 1 || !recorder.closed[0] || recorder.contents[0].String() != "main" {
		t.Errorf("archives = %+v %v %v", recorder.archives, recorder.closed, recorder.contents)
	}
	if server.canceled() != 1 {
		t.Errorf("%d cancel requests, want 1", server.canceled())
	}
	if conn.TxStatus() != 'I' {
		t.Errorf("the connection was not read through to ReadyForQuery")
	}
}

// A write that fails closes the archive it was writing.
func TestBaseBackupWriteFails(t *testing.T) {
	conn, server := scriptedConn(t, "16.1")
	serve(server, backupScript(copyData('n', "base.tar\x00", "\x00"), copyData('d', "main"), copyData('d', "more")))
	full := errors.New("disk full")
	closed := false
	_, err := BaseBackup(t.Context(), conn, BaseBackupOptions{}, func(Archive) (io.WriteCloser, error) {
		return &closeRecorder{failingArchive{full}, &closed}, nil
	})
	if !errors.Is(err, full) || !closed {
		t.Fatalf("err = %v, closed %v", err, closed)
	}
}

type closeRecorder struct {
	io.WriteCloser
	closed *bool
}

func (c *closeRecorder) Close() error { *c.closed = true; return c.WriteCloser.Close() }

// A server that fails partway, as one does on a checksum failure or when it
// is cancelled, fails the backup with its error.
func TestBaseBackupServerFails(t *testing.T) {
	conn, server := scriptedConn(t, "16.1")
	script := backupScript(copyData('n', "base.tar\x00", "\x00"))
	script = script[:bytes.LastIndex(script, pgMessage('c', nil))]
	script = append(script, pgMessage('E', []byte("SERROR\x00C57014\x00Mcanceling statement due to user request\x00\x00"))...)
	script = append(script, pgMessage('Z', []byte{'I'})...)
	serve(server, script)
	_, err := BaseBackup(t.Context(), conn, BaseBackupOptions{}, (&archiveRecorder{}).open)
	var serverErr *wire.Error
	if !errors.As(err, &serverErr) || serverErr.Code != "57014" {
		t.Fatalf("err = %v, want the server's 57014", err)
	}
}

// What a server sends that is not a backup fails as malformed, and does not
// reach the caller as data.
func TestBaseBackupMalformed(t *testing.T) {
	tests := []struct {
		name string
		copy [][]byte
	}{
		{"data before an archive", [][]byte{copyData('d', "x")}},
		{"an unknown message", [][]byte{copyData('x')}},
		{"an empty message", [][]byte{pgMessage('d', nil)}},
		{"a short progress report", [][]byte{copyData('p', "1234")}},
		{"a long progress report", [][]byte{copyData('p', "123456789")}},
		{"an archive with no name", [][]byte{copyData('n', "\x00", "\x00")}},
		{"an archive name not ended", [][]byte{copyData('n', "base.tar")}},
		{"an archive with no tablespace", [][]byte{copyData('n', "base.tar\x00")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, server := scriptedConn(t, "18.0")
			serve(server, backupScript(tt.copy...))
			recorder := &archiveRecorder{}
			_, err := BaseBackup(t.Context(), conn, BaseBackupOptions{}, recorder.open)
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
			if len(recorder.contents) > 0 && recorder.contents[0].Len() > 0 {
				t.Errorf("wrote %q", recorder.contents[0])
			}
		})
	}
}

// A server older than 15 is refused before anything is sent: its archives
// come in a format of its own, which would be written out as garbage.
func TestBaseBackupOldServer(t *testing.T) {
	conn, server := scriptedConn(t, "14.12")
	query := serve(server, nil)
	_, err := BaseBackup(t.Context(), conn, BaseBackupOptions{}, (&archiveRecorder{}).open)
	if !errors.Is(err, ErrBaseBackupVersion) {
		t.Fatalf("err = %v, want ErrBaseBackupVersion", err)
	}
	_ = server.Close()
	if _, sent := <-query; sent {
		t.Error("a command was sent")
	}
}

func liveBackupConn(t *testing.T) *wire.Conn {
	t.Helper()
	conn := liveConn(t, "true")
	if !backupProtocol(conn.ParameterStatus("server_version")) {
		t.Skipf("BASE_BACKUP needs PostgreSQL 15, the server is %s", conn.ParameterStatus("server_version"))
	}
	return conn
}

// tarFiles is an archive written into a tar reader, with the names and
// contents of the files it holds, up to a few kilobytes of each.
type tarFiles struct {
	pipe  *io.PipeWriter
	done  chan struct{}
	files map[string]string
	err   error
}

func readTar(decompress func(io.Reader) (io.Reader, error)) *tarFiles {
	reader, writer := io.Pipe()
	files := &tarFiles{pipe: writer, done: make(chan struct{}), files: map[string]string{}}
	go func() {
		defer close(files.done)
		defer func() { _, _ = io.Copy(io.Discard, reader) }()
		var source io.Reader = reader
		if decompress != nil {
			if source, files.err = decompress(reader); files.err != nil {
				return
			}
		}
		archive := tar.NewReader(source)
		for {
			header, err := archive.Next()
			if err != nil {
				files.err = err
				return
			}
			head, _ := io.ReadAll(io.LimitReader(archive, 4096))
			files.files[header.Name] = string(head)
		}
	}()
	return files
}

func (f *tarFiles) Write(p []byte) (int, error) { return f.pipe.Write(p) }
func (f *tarFiles) Close() error {
	_ = f.pipe.Close()
	<-f.done
	return nil
}

// A base backup of the server arrives as a tar of its data directory, with
// its label, and a manifest, and says where in the WAL it starts and ends.
func TestLiveBaseBackup(t *testing.T) {
	for _, compression := range []string{"", "gzip"} {
		t.Run("compression="+compression, func(t *testing.T) {
			conn := liveBackupConn(t)
			var decompress func(io.Reader) (io.Reader, error)
			if compression == "gzip" {
				decompress = func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) }
			}
			archives := map[string]*tarFiles{}
			var manifest bytes.Buffer
			open := func(archive Archive) (io.WriteCloser, error) {
				if archive.Manifest {
					return nopCloser{&manifest}, nil
				}
				files := readTar(decompress)
				archives[archive.Name] = files
				return files, nil
			}
			reports := 0
			options := BaseBackupOptions{Label: "caduceus test", Manifest: "yes", FastCheckpoint: true, NoWait: true, WAL: true, Compression: compression, Progress: func(int64) { reports++ }}
			backup, err := BaseBackup(t.Context(), conn, options, open)
			if err != nil {
				t.Fatal(err)
			}
			name := "base.tar"
			if compression == "gzip" {
				name = "base.tar.gz"
			}
			base := archives[name]
			if base == nil {
				t.Fatalf("archives %v, no %s", archives, name)
			}
			if base.err != io.EOF {
				t.Fatalf("reading %s: %v", name, base.err)
			}
			if _, ok := base.files["PG_VERSION"]; !ok || !strings.Contains(base.files["backup_label"], "LABEL: caduceus test") {
				t.Errorf("%s holds %d files, backup_label %q", name, len(base.files), base.files["backup_label"])
			}
			if !strings.Contains(manifest.String(), "PostgreSQL-Backup-Manifest-Version") {
				t.Errorf("manifest %.100q", manifest.String())
			}
			if backup.Start == 0 || backup.End < backup.Start || backup.StartTimeline < 1 || len(backup.Tablespaces) == 0 || backup.Tablespaces[0].Size <= 0 || reports == 0 {
				t.Errorf("backup = %+v, %d progress reports", backup, reports)
			}
		})
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// failingArchive fails its first write.
type failingArchive struct{ err error }

func (f failingArchive) Write([]byte) (int, error) { return 0, f.err }
func (failingArchive) Close() error                { return nil }

// A writer that fails stops the backup on the server, rather than reading
// the whole of it to throw away, and the connection can be used again.
func TestLiveBaseBackupWriterFails(t *testing.T) {
	conn := liveBackupConn(t)
	full := errors.New("disk full")
	_, err := BaseBackup(t.Context(), conn, BaseBackupOptions{FastCheckpoint: true, NoWait: true}, func(Archive) (io.WriteCloser, error) {
		return failingArchive{full}, nil
	})
	if !errors.Is(err, full) {
		t.Fatalf("err = %v, want the writer's", err)
	}
	if _, err := IdentifySystem(t.Context(), conn); err != nil {
		t.Fatalf("the connection after: %v", err)
	}
}
