// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "testing"

// calmDown runs enough exchanges that read next to nothing for a grown buffer
// to be given back at the next opportunity.
func calmDown(t *testing.T, server *pacedConn, conn *Conn) {
	t.Helper()
	for range calmExchanges {
		server.send(readyForQuery())
		readToReady(t, conn)
	}
}

// A result read after a run of short exchanges had its buffer taken away
// whenever it stood empty, and grown again by the read after: the count of calm
// exchanges that allows a buffer to be given back still stood, and nothing said
// that the exchange in hand was using it. Rows of 128 bytes, which leave the
// buffer empty after every 8 KiB a server sends, had it replaced 4,168 times
// over 400,000 rows: 49 MiB of garbage for a result of 51 MiB.
func TestReadBufferIsNotGivenBackInTheMiddleOfAResult(t *testing.T) {
	for _, shape := range []struct {
		name            string
		pace, row, rows int
	}{
		{"rows that divide a server buffer", initialReadBuffer, 128, 400_000},
		{"rows that do not", initialReadBuffer, 17, 1_000_000},
		{"reads of every size", 3 * initialReadBuffer, 17, 1_000_000},
		{"wide rows in small reads", 1448, 12 << 10, 2_000},
	} {
		t.Run(shape.name, func(t *testing.T) {
			server := &pacedConn{pace: streamReadBuffer}
			conn := standIn(server, &Config{})

			// A stream to grow the buffer, then quiet enough to let it go.
			server.send(dataRows(8_000, 128), readyForQuery())
			readToReady(t, conn)
			if len(conn.rbuf) != streamReadBuffer {
				t.Fatalf("buffer after a megabyte of rows = %d, want %d", len(conn.rbuf), streamReadBuffer)
			}
			calmDown(t, server, conn)

			server.pace = shape.pace
			server.send(dataRows(shape.rows, shape.row), readyForQuery())
			// Grown as the result asks, through 16, 32 and 64 KiB at the most.
			if replaced := readToReady(t, conn); replaced > 3 {
				t.Fatalf("the buffer was replaced %d times while one result was read, want at most 3", replaced)
			}
		})
	}
}

// A buffer the connection had no more use for was let go at its next read: one
// grown for a row past shrinkAbove at the read after that row, one a stream
// grew at the first read after the calm exchanges that count against it. A
// connection goes back to its pool when an exchange ends and may sit there,
// and it sat there holding the megabytes of the last wide row it read.
func TestReadBufferIsGivenBackWhenTheExchangeEnds(t *testing.T) {
	server := &pacedConn{pace: streamReadBuffer}
	conn := standIn(server, &Config{})

	server.send(dataRows(1, 2*shrinkAbove), readyForQuery())
	readToReady(t, conn)
	if len(conn.rbuf) != initialReadBuffer {
		t.Fatalf("buffer once a row of %d bytes has been read = %d, want %d", 2*shrinkAbove, len(conn.rbuf), initialReadBuffer)
	}

	server.send(dataRows(8_000, 128), readyForQuery())
	readToReady(t, conn)
	if len(conn.rbuf) != streamReadBuffer {
		t.Fatalf("buffer after a megabyte of rows = %d, want %d", len(conn.rbuf), streamReadBuffer)
	}
	calmDown(t, server, conn)
	if len(conn.rbuf) != initialReadBuffer {
		t.Fatalf("buffer after %d exchanges that read next to nothing = %d, want %d", calmExchanges, len(conn.rbuf), initialReadBuffer)
	}
}

// What keeps a buffer a stream grew is an exchange the initial buffer could not
// have held, not a row that happens to be a good share of the buffer.
func TestReadBufferAStreamGrewIsGivenBackToRowsTheInitialOneHolds(t *testing.T) {
	server := &pacedConn{pace: initialReadBuffer}
	conn := standIn(server, &Config{})

	server.send(dataRows(96, 128), readyForQuery())
	readToReady(t, conn)
	if len(conn.rbuf) != 2*initialReadBuffer {
		t.Fatalf("buffer after a result of 12 KiB = %d, want %d", len(conn.rbuf), 2*initialReadBuffer)
	}
	// In segments, so that each row is still on its way when it is asked for.
	server.pace = 1448
	for range calmExchanges {
		server.send(dataRows(1, 5<<10), readyForQuery())
		readToReady(t, conn)
	}
	if len(conn.rbuf) != initialReadBuffer {
		t.Fatalf("buffer after %d rows of 5 KiB = %d, want %d", calmExchanges, len(conn.rbuf), initialReadBuffer)
	}
}

// A read that filled the buffer was taken to mean that more was waiting than
// there was room for, whatever it brought. The last few hundred bytes of a
// segment fill a buffer too, when what came before them has not been moved
// out of the way, so a result that trickled in over a slow link grew the
// buffer to its full 64 KiB, eight times what such a result is read through.
func TestReadBufferDoesNotGrowForAResultThatTricklesIn(t *testing.T) {
	server := &pacedConn{pace: 1448}
	conn := standIn(server, &Config{})

	server.send(dataRows(8_000, 128), readyForQuery())
	if replaced := readToReady(t, conn); replaced != 0 || len(conn.rbuf) != initialReadBuffer {
		t.Fatalf("a megabyte in reads of 1448 bytes: the buffer was replaced %d times and is %d bytes, want it left at %d",
			replaced, len(conn.rbuf), initialReadBuffer)
	}
}

// A buffer grown for one wide row was kept for as long as the results after it
// arrived in reads of 8 KiB or more, which is any result of more than a few
// kilobytes: such a read counted as a use of the buffer whatever the buffer's
// size, so the run of calm exchanges that gives one back never began. One row
// of 200 KiB left a connection holding 200 KiB through 2,000 results of 12 KiB
// that a 16 KiB buffer serves, and a pool holds that once per connection.
func TestReadBufferGrownForOneWideRowIsGivenBack(t *testing.T) {
	server := &pacedConn{pace: initialReadBuffer}
	conn := standIn(server, &Config{})

	const wide = 200 << 10
	server.send(dataRows(1, wide), readyForQuery())
	readToReady(t, conn)
	if len(conn.rbuf) < wide {
		t.Fatalf("buffer after a row of %d bytes = %d", wide, len(conn.rbuf))
	}

	result := func() int {
		server.send(dataRows(96, 128), readyForQuery())
		return readToReady(t, conn)
	}
	for range 2 * calmExchanges {
		result()
	}
	if len(conn.rbuf) != 2*initialReadBuffer {
		t.Fatalf("buffer after %d results of 12 KiB = %d, want the %d they are read through",
			2*calmExchanges, len(conn.rbuf), 2*initialReadBuffer)
	}
	// And that one is theirs to keep.
	for n := range 100 {
		if replaced := result(); replaced != 0 {
			t.Fatalf("result %d after it settled: the buffer was replaced %d times", n, replaced)
		}
	}
}

// What must not be lost with either fix: a connection that keeps reading
// results larger than the initial buffer keeps the buffer it reads them
// through, whether they arrive as a stream of rows or as one row, in reads of
// a server buffer or of a network segment.
func TestReadBufferIsKeptByResultsThatUseIt(t *testing.T) {
	for _, shape := range []struct {
		name            string
		pace, row, rows int
	}{
		{"a stream in server buffers", initialReadBuffer, 128, 96},
		{"a stream in segments", 1448, 128, 96},
		{"one row in server buffers", initialReadBuffer, 12 << 10, 1},
		{"one row in segments", 1448, 12 << 10, 1},
		{"a megabyte at once", streamReadBuffer, 128, 8_000},
	} {
		t.Run(shape.name, func(t *testing.T) {
			server := &pacedConn{pace: shape.pace}
			conn := standIn(server, &Config{})

			result := func() int {
				server.send(dataRows(shape.rows, shape.row), readyForQuery())
				return readToReady(t, conn)
			}
			result()
			grown := len(conn.rbuf)
			for n := range 10 * calmExchanges {
				if replaced := result(); replaced != 0 || len(conn.rbuf) != grown {
					t.Fatalf("result %d: the buffer was replaced %d times and is %d bytes, want it kept at %d",
						n, replaced, len(conn.rbuf), grown)
				}
			}
		})
	}
}

// The other half of giving back a buffer one wide row left behind: rows that
// are wide themselves, of whatever sizes, keep the buffer they are read
// through, and are not made to grow it again every few exchanges.
func TestReadBufferGrownForWideRowsIsKeptByWideRows(t *testing.T) {
	server := &pacedConn{pace: initialReadBuffer}
	conn := standIn(server, &Config{})

	row := func(size int) int {
		server.send(dataRows(1, size), readyForQuery())
		return readToReady(t, conn)
	}
	row(200 << 10)
	grown := len(conn.rbuf)
	for n := range 10 * calmExchanges {
		size := []int{60 << 10, 180 << 10, 100 << 10, 75 << 10}[n%4]
		if replaced := row(size); replaced != 0 || len(conn.rbuf) != grown {
			t.Fatalf("row %d, of %d bytes: the buffer was replaced %d times and is %d bytes, want it kept at %d",
				n, size, replaced, len(conn.rbuf), grown)
		}
	}
}
