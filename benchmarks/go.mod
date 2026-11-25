module github.com/gsoultan/caduceus/benchmarks

go 1.26.5

// pgx, pglogrepl and database/sql are here, and only here, as what caduceus is
// measured against. caduceus itself imports none of them, and an application
// using caduceus never downloads this module.
require github.com/gsoultan/caduceus v0.0.0

require (
	github.com/jackc/pglogrepl v0.0.0-20260401131349-e37c41485510
	github.com/jackc/pgx/v5 v5.10.0
	github.com/lib/pq v1.10.9
)

require (
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/text v0.35.0 // indirect
)

replace github.com/gsoultan/caduceus => ../
