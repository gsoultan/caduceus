// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/caduceus/codec"
)

func TestAppendCopyTuple(t *testing.T) {
	got, err := appendCopyTuple(nil, []uint32{codec.Int4, codec.Text, codec.Int8}, []any{1, "ab", nil})
	want := []byte{
		0, 3, // three fields
		0, 0, 0, 4, 0, 0, 0, 1, // int4 1
		0, 0, 0, 2, 'a', 'b', // text
		0xff, 0xff, 0xff, 0xff, // NULL
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("appendCopyTuple = %v, %v\nwant %v", got, err, want)
	}
}

// A value with no binary form is reported as such and names its column, so the
// stream can tell it from a value that cannot be sent at all.
func TestAppendCopyTupleReportsWhatHasNoBinaryForm(t *testing.T) {
	_, err := appendCopyTuple(nil, []uint32{codec.Int4, codec.Int4}, []any{1, "2"})
	var argument *ArgumentError
	if !errors.Is(err, codec.ErrNoBinary) || !errors.As(err, &argument) || argument.Index != 1 {
		t.Fatalf("appendCopyTuple = %v, want ErrNoBinary for argument 1", err)
	}
}

// messages lists the type bytes of the frontend messages in buf, with the SQL
// of each Parse, which is enough to see what a stream would send.
func messages(t *testing.T, buf []byte) []string {
	t.Helper()
	var out []string
	for len(buf) > 0 {
		if len(buf) < 5 {
			t.Fatalf("truncated message: %v", buf)
		}
		size := int(buf[1])<<24 | int(buf[2])<<16 | int(buf[3])<<8 | int(buf[4])
		body := buf[5 : 1+size]
		name := string(buf[0])
		if buf[0] == 'P' {
			name += " " + string(body[1:bytes.IndexByte(body[1:], 0)+1])
		}
		out = append(out, name)
		buf = buf[1+size:]
	}
	return out
}

func streamOf(oids ...uint32) *copyStream {
	stream := &copyStream{target: "t (a, b)", oids: oids, binary: binaryColumns(oids)}
	stream.begin()
	return stream
}

func encodeAll(t *testing.T, stream *copyStream, rows ...[]any) {
	t.Helper()
	for _, row := range rows {
		if err := stream.encode(row); err != nil {
			t.Fatalf("encode(%v) = %v", row, err)
		}
		stream.written++
	}
	stream.end()
}

func TestCopyStreamStaysBinaryForNativeValues(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Text)
	encodeAll(t, stream, []any{int64(1), "a"}, []any{nil, "b"})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "P COPY t (a, b) FROM STDIN (FORMAT binary) | B | E | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
}

// A column type the codec has no binary encoding for makes the whole COPY text,
// from the start.
func TestCopyStreamIsTextForATypeWithoutBinary(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Numeric)
	encodeAll(t, stream, []any{int64(1), "1.50"})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "P COPY t (a, b) FROM STDIN | B | E | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
}

// A source of strings, as read from a CSV file, never had a binary row: the
// binary COPY it opened with is replaced rather than sent empty.
func TestCopyStreamReplacesAnUnusedBinaryCopy(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Text)
	encodeAll(t, stream, []any{"1", "a"}, []any{"2", "b"})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "P COPY t (a, b) FROM STDIN | B | E | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
}

// A string for an integer column part way through cannot go in binary, and
// refusing it would fail a COPY the server could have parsed. The stream ends
// the binary COPY with the rows it has and carries on in a text one, and stays
// in text after that.
func TestCopyStreamChangesToTextMidStream(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Text)
	encodeAll(t, stream, []any{int64(1), "a"}, []any{"2", "b"}, []any{int64(3), "c"})
	got := strings.Join(messages(t, stream.buf), " | ")
	want := "P COPY t (a, b) FROM STDIN (FORMAT binary) | B | E | d | c | P COPY t (a, b) FROM STDIN | B | E | d | c"
	if got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
	if !bytes.Contains(stream.buf, []byte("2\tb\n3\tc\n")) {
		t.Fatalf("text rows missing from %q", stream.buf)
	}
}

func TestCopyStreamEmptySource(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Text)
	encodeAll(t, stream)
	if !bytes.Contains(stream.buf, []byte(copySignature+"\xff\xff")) {
		t.Fatalf("an empty binary COPY should be its header and trailer, got %q", stream.buf)
	}
	text := streamOf(codec.Numeric)
	encodeAll(t, text)
	if got, want := strings.Join(messages(t, text.buf), " | "), "P COPY t (a, b) FROM STDIN | B | E | c"; got != want {
		t.Fatalf("empty text stream sent %q, want %q", got, want)
	}
}

// A nil source used to be called into, which is a nil pointer dereference in
// the caller's goroutine. It is refused before the connection is touched.
func TestCopyFromRefusesANilSource(t *testing.T) {
	if _, err := new(Conn).CopyFrom(t.Context(), []string{"t"}, []string{"a"}, nil); !errors.Is(err, ErrNoCopySource) {
		t.Fatalf("CopyFrom(nil source) = %v, want ErrNoCopySource", err)
	}
}
