// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// batchWindow is one part of a batch that is sent in parts: the statements
// from first on, whose encoded messages end at end in the batch's buffer.
// alone is a window that begins with a statement too large to share one,
// which is sent only once every statement before it has been read.
type batchWindow struct {
	first int
	end   int
	alone bool
}
