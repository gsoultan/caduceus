// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "fmt"

// TypeError is a column that cannot be read into the destination it was
// scanned into: the codec does not read its type, in the format it arrived
// in, as that destination.
type TypeError struct {
	// OID is the column's type.
	OID uint32
	// Destination is what it was to be read as, such as *float64.
	Destination string
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("caduceus/codec: cannot scan type %d into %s", e.OID, e.Destination)
}
