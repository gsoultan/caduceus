// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// RowToFunc turns the current row into a T. CollectRows and its kin call it
// once for each row; RowTo, RowToMap and the RowToStruct functions are ones
// ready made.
type RowToFunc[T any] func(row CollectableRow) (T, error)
