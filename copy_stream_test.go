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
// of each Query, which is enough to see what a stream would send.
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
		if buf[0] == 'Q' {
			name += " " + string(body[:len(body)-1])
		}
		out = append(out, name)
		buf = buf[1+size:]
	}
	return out
}

func streamOf(oids ...uint32) *copyStream {
	stream := &copyStream{target: "t (a, b)", oids: oids, binary: true}
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
	if want := "Q COPY t (a, b) FROM STDIN (FORMAT binary) | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
}

// A column type the codec has no binary encoding for makes the whole COPY text,
// from the start.
func TestCopyStreamIsTextForATypeWithoutBinary(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Numeric)
	encodeAll(t, stream, []any{int64(1), "1.50"})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "Q COPY t (a, b) FROM STDIN | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
}

// A source of strings, as read from a CSV file, never had a binary row: the
// binary COPY it opened with is replaced rather than sent empty.
func TestCopyStreamReplacesAnUnusedBinaryCopy(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Text)
	encodeAll(t, stream, []any{"1", "a"}, []any{"2", "b"})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "Q COPY t (a, b) FROM STDIN | d | c"; got != want {
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
	want := "Q COPY t (a, b) FROM STDIN (FORMAT binary) | d | c | Q COPY t (a, b) FROM STDIN | d | c"
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
	// With no row to say otherwise, a column codec cannot write in binary
	// still gets the binary header: an empty binary COPY is as valid as an
	// empty text one.
	numeric := streamOf(codec.Numeric)
	encodeAll(t, numeric)
	if got, want := strings.Join(messages(t, numeric.buf), " | "), "Q COPY t (a, b) FROM STDIN (FORMAT binary) | d | c"; got != want {
		t.Fatalf("empty stream sent %q, want %q", got, want)
	}
}

// A BinaryParam writes itself in binary for a type codec has no encoder for,
// and it has no text form, so the stream must not decide on text from the
// column types alone.
func TestCopyStreamStaysBinaryForABinaryParam(t *testing.T) {
	const tstzrange = 3910
	stream := streamOf(tstzrange)
	encodeAll(t, stream, []any{rawParam{1}}, []any{nil})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "Q COPY t (a, b) FROM STDIN (FORMAT binary) | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
}

// A first row that cannot be written in binary turns the stream to text
// before anything went out: one COPY, in text.
func TestCopyStreamStartsInTextWhenTheFirstRowNeedsIt(t *testing.T) {
	stream := streamOf(codec.Numeric)
	encodeAll(t, stream, []any{"1.5"}, []any{"2"})
	got := strings.Join(messages(t, stream.buf), " | ")
	if want := "Q COPY t (a, b) FROM STDIN | d | c"; got != want {
		t.Fatalf("stream sent %q\nwant %q", got, want)
	}
	if !bytes.Contains(stream.buf, []byte("1.5\n2\n")) {
		t.Fatalf("text rows missing from %q", stream.buf)
	}
}

// rawParam is a BinaryParam that writes one byte.
type rawParam struct{ b byte }

func (p rawParam) AppendBinaryParam(dst []byte, _ uint32) ([]byte, bool, error) {
	return append(dst, p.b), false, nil
}

// Every statement sent is answered with a ReadyForQuery, and reading one too
// few or one too many leaves the connection out of step with the server. A
// statement still in the buffer has no answer coming; a transaction of the
// stream's own has one more, for the COMMIT or ROLLBACK that ends it.
func TestCopyStreamCountsTheRepliesItIsOwed(t *testing.T) {
	stream := streamOf(codec.Int8, codec.Text)
	if got := stream.owed(); got != 0 {
		t.Fatalf("owed = %d before anything is sent, want 0", got)
	}
	stream.own = true
	if got := stream.owed(); got != 1 {
		t.Fatalf("owed = %d for an unsent COPY in its own transaction, want 1", got)
	}
	stream.unsent = 0 // as a flush leaves it
	for _, row := range [][]any{{int64(1), "a"}, {"2", "b"}} {
		if err := stream.encode(row); err != nil {
			t.Fatal(err)
		}
		stream.written++
	}
	if stream.statements != 2 || stream.owed() != 2 {
		t.Fatalf("statements = %d, owed = %d with the second COPY unsent, want 2 and 2", stream.statements, stream.owed())
	}
	stream.unsent = 0
	if got := stream.owed(); got != 3 {
		t.Fatalf("owed = %d once both are sent, want 3", got)
	}

	replaced := streamOf(codec.Int8, codec.Text)
	if err := replaced.encode([]any{"1", "a"}); err != nil {
		t.Fatal(err)
	}
	if replaced.statements != 1 || replaced.unsent != 1 {
		t.Fatalf("a replaced binary COPY left statements = %d, unsent = %d, want 1 and 1", replaced.statements, replaced.unsent)
	}
}

// A nil source used to be called into, which is a nil pointer dereference in
// the caller's goroutine. It is refused before the connection is touched.
func TestCopyFromRefusesANilSource(t *testing.T) {
	if _, err := new(Conn).CopyFrom(t.Context(), []string{"t"}, []string{"a"}, nil); !errors.Is(err, ErrNoCopySource) {
		t.Fatalf("CopyFrom(nil source) = %v, want ErrNoCopySource", err)
	}
}
