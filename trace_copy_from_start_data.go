// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// TraceCopyFromStartData is a CopyFrom about to run.
type TraceCopyFromStartData struct {
	TableName   Identifier
	ColumnNames []string
}
