# Graph Report - caduceus  (2026-11-27)

## Corpus Check
- 236 files · ~126,886 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 2 file(s) not represented in the graph (top: (none) 2)

## Summary
- 1640 nodes · 7205 edges · 72 communities (54 shown, 18 thin omitted)
- Extraction: 80% EXTRACTED · 20% INFERRED · 0% AMBIGUOUS · INFERRED: 1410 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `782589e6`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Config
- testing.T
- driver_test.go
- recorder
- restated_test.go
- scan.go
- cdc_decode_test.go
- Conn
- layoutCache
- Tx
- copyStream
- live
- context.Context
- PipelineResults
- LSN
- time.Time
- Conn
- liveTable
- Options
- TestLiveRunPreparedByName
- standIn
- .queryText
- encode.go
- Connect
- authenticator
- testing.B
- Rows
- codec_test.go
- ParseConfig
- BatchResults
- reader
- statement
- array.go
- ProtocolVersion
- CollectableRow
- lexer.go
- range_text.go
- .collect
- CommandTag
- replication_test.go
- .server
- .QueryRow
- text.go
- .login
- CollectOneRow
- built
- Decoder
- .Wire
- net.Conn
- textArray
- passwordFromFile
- Error
- Row
- .RewriteQuery
- .WaitForNotification
- Hstore
- encodeAll
- buildConfig
- .Run
- copy_raw_test.go
- appendPoints
- appendNumber
- TestLiveBatchQueue
- pgoutput_options.go
- notSentError
- timedOut
- ArgumentError
- ConnectError
- columnEncoder
- columnScanner
- TypeError
- github.com/gsoultan/caduceus

## God Nodes (most connected - your core abstractions)
1. `Conn` - 116 edges
2. `liveConn()` - 95 edges
3. `Config` - 95 edges
4. `Conn` - 89 edges
5. `What it does` - 63 edges
6. `Field` - 55 edges
7. `watermark` - 52 edges
8. `Rows` - 49 edges
9. `attempt` - 48 edges
10. `Guard` - 46 edges

## Surprising Connections (you probably didn't know these)
- `What it does` --references--> `CompositeFields`  [INFERRED]
  README.md → codec/composite_fields.go
- `What it does` --references--> `Hstore`  [INFERRED]
  README.md → codec/hstore.go
- `What it does` --references--> `ValidSlotName()`  [INFERRED]
  README.md → replication/commands.go
- `What it does` --references--> `IdentifySystem()`  [INFERRED]
  README.md → replication/commands.go
- `What it does` --references--> `CreateReplicationSlot()`  [INFERRED]
  README.md → replication/commands.go

## Import Cycles
- None detected.

## Communities (72 total, 18 thin omitted)

### Community 0 - "Config"
Cohesion: 0.17
Nodes (31): layoutAddress, layoutAudit, layoutBase, layoutPair, layoutShadow, layoutShadowFirst, layoutSkippedEmbed, layoutTaggedEmbed (+23 more)

### Community 1 - "testing.T"
Cohesion: 0.07
Nodes (61): TestLiveCheckConn(), checkUsers(), TestLiveAppendRowsKeepsTheSlice(), TestLiveCollectOneRow(), TestLiveCollectRowsByName(), TestLiveCommandTags(), TestLiveForEachRow(), TestLiveRowToScalarsAndMaps() (+53 more)

### Community 2 - "driver_test.go"
Cohesion: 0.06
Nodes (11): ArgumentCountError, batchWindow, QueryRewriter, TestCommandTag(), CopyFromSlice(), TestQuoting(), rewrites(), newStatementCache() (+3 more)

### Community 3 - "recorder"
Cohesion: 0.07
Nodes (21): BatchTracer, ConnectTracer, CopyFromTracer, Identifier, PrepareTracer, QueryTracer, recorder, TraceBatchEndData (+13 more)

### Community 4 - "restated_test.go"
Cohesion: 0.21
Nodes (16): Bits, Box, Circle, geometryNumbers(), pointsOf(), Line, Lseg, Multirange (+8 more)

### Community 5 - "scan.go"
Cohesion: 0.11
Nodes (41): be16(), be32(), be64(), binaryFloat(), binaryInt(), binaryInterval(), binaryTime(), microsTime() (+33 more)

### Community 6 - "cdc_decode_test.go"
Cohesion: 0.05
Nodes (17): BenchmarkDecodeUpdate(), benchmarkPglogreplDecodeUpdate(), columnMap(), relationRecord(), updateRecord(), DeleteMessage, InsertMessage, RelationColumn (+9 more)

### Community 7 - "Conn"
Cohesion: 0.10
Nodes (7): Conn, outdated(), isServerError(), firstError(), unshift(), AppendCopyDone(), AppendQuery()

### Community 8 - "layoutCache"
Cohesion: 0.08
Nodes (28): layoutCache, layoutKey, mapping, structField, structLayout, keepable(), newLayoutCache(), withoutUnderscores() (+20 more)

### Community 9 - "Tx"
Cohesion: 0.10
Nodes (14): inTransaction, IsoLevel, StatementDescription, Tx, TxBeginner, TxOptions, TestLiveSerializableOnAStandby(), TestLiveSpentTransactionCannotTouchALaterOne() (+6 more)

### Community 10 - "copyStream"
Cohesion: 0.08
Nodes (10): copyFunc, copyRequest, copySlice, CopySource, copyStream, failingSource, rowSlice, CopyFromFunc() (+2 more)

### Community 11 - "live"
Cohesion: 0.06
Nodes (37): name, runs-on, steps, env, DATABASE_URL, GO_VERSION, POSTGRES_PASSWORD, jobs (+29 more)

### Community 12 - "context.Context"
Cohesion: 0.13
Nodes (30): CreateReplicationSlot(), createSlotCommand(), DropReplicationSlot(), IdentifySystem(), oneRow(), ReadReplicationSlot(), SendStandbyStatusUpdate(), startCommand() (+22 more)

### Community 13 - "PipelineResults"
Cohesion: 0.11
Nodes (8): sameSQL(), statementAt(), PipelineResults, Query, inBinary(), restate(), NotSent(), SafeToRetry()

### Community 14 - "LSN"
Cohesion: 0.08
Nodes (17): AppendStandbyStatusUpdate(), LogicalDecodingMessage, LSN, appendHex(), TestAppendStandbyStatusUpdate(), OriginMessage, TestLSNRoundTrips(), TestStreamMessages() (+9 more)

### Community 15 - "time.Time"
Cohesion: 0.07
Nodes (14): collectAudit, collectUser, parseTimestampText(), parseWithZone(), untypedTime(), withEra(), BeginMessage, BeginPrepareMessage (+6 more)

### Community 17 - "liveTable"
Cohesion: 0.13
Nodes (28): TestLiveCopyRawFailures(), CopyFromRows(), TestLiveFirstStatementRunsInsideTheTransaction(), TestLiveFirstStatementThatCannotBeParsed(), TestLiveRefusedBeginRunsNothing(), TestLiveTransactionRoundTrips(), TestLiveUnwind(), countRows() (+20 more)

### Community 18 - "Options"
Cohesion: 0.11
Nodes (23): Options, sentMessage, TestLivePrepareBehindPooler(), TestLivePrepareCancelled(), TestLivePreparedNeverEvicted(), Connect(), connect(), ConnectConfig() (+15 more)

### Community 19 - "TestLiveRunPreparedByName"
Cohesion: 0.17
Nodes (21): QuoteIdentifier(), copyTable(), TestLiveCopyBinaryTypes(), TestLiveCopyTextForOtherTypes(), TestLiveStatementEndedByALeftoverDeadlineClosesTheConnection(), TestLiveOversizedStatementWaitsForTheResultsInFrontOfIt(), TestLivePrepareAgain(), TestLiveRunPreparedByName() (+13 more)

### Community 20 - "standIn"
Cohesion: 0.16
Nodes (23): cancelRequests(), TestDeadlineOfTheContextEndsTheExchangeAsTheContexts(), TestExchangeEndedByADeadlineNotItsOwnClosesTheConnection(), TestTimeoutBeforeAnExchangeIsNotHeldAgainstIt(), TestTimeoutFromElsewhereLeavesTheConnectionAlone(), TestTimeoutThatIsNotTheContextsIsNotReportedAsIts(), newLateConn(), standIn() (+15 more)

### Community 21 - ".queryText"
Cohesion: 0.19
Nodes (18): flushBefore(), appendBind(), appendParsed(), oversized(), AppendClose(), AppendCopyData(), AppendCopyFail(), AppendDescribe() (+10 more)

### Community 22 - "encode.go"
Cohesion: 0.13
Nodes (20): TestEncodeRefusesFloatOverflowingReal(), TestScanRefusesOverflow(), Encode(), encodeBool(), encodeBytes(), encodeDuration(), encodeFloat(), encodeInt() (+12 more)

### Community 23 - "Connect"
Cohesion: 0.16
Nodes (26): Connect(), TestLiveConnectHooks(), TestLiveEveryServerFailing(), TestLiveFallbackToTheNextServer(), liveConfig(), liveConn(), TestLiveConnectAndQuery(), TestLiveContextCancelsTheQuery() (+18 more)

### Community 24 - "authenticator"
Cohesion: 0.13
Nodes (11): authenticator, authPolicy, md5Response(), offersMechanism(), scramMechanisms(), TestEndPoint(), TestSCRAMMechanisms(), challengeFields() (+3 more)

### Community 25 - "testing.B"
Cohesion: 0.21
Nodes (22): contender, BenchmarkBatch10(), BenchmarkCopy1000(), BenchmarkInsert1000InTransaction(), BenchmarkQueryRow(), BenchmarkQueryRowNothingKept(), BenchmarkRows100(), BenchmarkTransaction() (+14 more)

### Community 26 - "Rows"
Cohesion: 0.15
Nodes (7): readTo, Rows, nullString, ForEachRow(), forRow(), Behind a pooler, describes()

### Community 27 - "codec_test.go"
Cohesion: 0.10
Nodes (20): BinaryInput(), EncodeBinary(), textIsBinary(), FuzzParseDecimalFast(), FuzzScan(), TestBinaryInput(), TestEncodeBinary(), TestEncodeTextFastPathMatchesTheGeneralPath() (+12 more)

### Community 28 - "ParseConfig"
Cohesion: 0.09
Nodes (22): TestLiveNoticeAndNotificationHandlers(), TestLiveConnectError(), parseRequireAuth(), liveServers(), TestFallbackTLSAndCopy(), TestLiveTargetSessionAttrs(), TestParseSeveralHosts(), TestParseTargetSessionAttrs() (+14 more)

### Community 30 - "reader"
Cohesion: 0.32
Nodes (3): ParseKeepalive(), Message, reader

### Community 31 - "statement"
Cohesion: 0.19
Nodes (7): statement, statementCache, columnTypes(), current(), plainName(), serverName(), describe()

### Community 32 - "array.go"
Cohesion: 0.22
Nodes (17): arrayText(), fillSlice(), naturalArray(), nest(), parseArray(), printedElement(), quoteArrayElement(), readArray() (+9 more)

### Community 33 - "ProtocolVersion"
Cohesion: 0.16
Nodes (10): connect(), connectOnce(), ctxErr(), dial(), fallbackWorthy(), retryVersion(), parseProtocolVersion(), versionBytes() (+2 more)

### Community 34 - "CollectableRow"
Cohesion: 0.22
Nodes (12): CollectableRow, RowTo(), RowToAddrOf(), RowToMap(), addrOf(), RowToAddrOfStructByName(), RowToAddrOfStructByNameLax(), RowToAddrOfStructByPos() (+4 more)

### Community 35 - "lexer.go"
Cohesion: 0.21
Nodes (12): namedRewrite, blockEnd(), dollarEnd(), escapeString(), identChar(), isDigit(), lineEnd(), literalEnd() (+4 more)

### Community 36 - "range_text.go"
Cohesion: 0.18
Nodes (13): BoundType, CompositeFields, TestParseRange(), splitRanges(), elementOf(), scanBound(), appendBound(), appendQuoted() (+5 more)

### Community 37 - ".collect"
Cohesion: 0.23
Nodes (14): newStatement(), be16(), be32(), cutString(), ParseParameterDescription(), ParseRowDescription(), SameRowDescription(), SplitDataRow() (+6 more)

### Community 38 - "CommandTag"
Cohesion: 0.12
Nodes (8): CommandTag, A million rows, caduceus, Concurrency, License, Packages, Performance, Testing

### Community 39 - "replication_test.go"
Cohesion: 0.37
Nodes (14): NewDecoder(), beginMessage(), BenchmarkDecodeUpdate(), changeMessage(), decoderWithUsers(), FuzzDecode(), relationMessage(), TestChangesDoNotShareMaps() (+6 more)

### Community 40 - ".server"
Cohesion: 0.15
Nodes (4): final(), connectError(), FallbackConfig, AppendTerminate()

### Community 41 - ".QueryRow"
Cohesion: 0.21
Nodes (4): LargeObject, LargeObjectMode, LargeObjects, eofIfEmpty()

### Community 42 - "text.go"
Cohesion: 0.22
Nodes (13): TestParseDecimalFastMatchesStrconv(), byteaEscaped(), decodeByteaEscape(), formatInterval(), formatTime(), indexByte(), infinite(), insertZone() (+5 more)

### Community 43 - ".login"
Cohesion: 0.21
Nodes (6): decryptKey(), hostTLS(), loadClientCertificate(), loadRoots(), validMode(), verifyChainOnly()

### Community 44 - "CollectOneRow"
Cohesion: 0.46
Nodes (10): RowToFunc, AppendRows(), collectable(), CollectExactlyOneRow(), CollectOneRow(), CollectRows(), noRow(), TestLiveCollectErrors() (+2 more)

### Community 45 - "built"
Cohesion: 0.18
Nodes (5): built, message(), TestParseMessage(), TestParseMessageRefusesMalformed(), ParseMessage()

### Community 46 - "Decoder"
Cohesion: 0.31
Nodes (8): Change, Decoder, decodeChange(), Op, Relation, parseRelation(), Transaction, parseBegin()

### Community 47 - ".Wire"
Cohesion: 0.17
Nodes (8): backendState(), smallSendBuffer(), containsName(), cutComma(), preparedOn(), TestLiveRefusedBeginStillClosesEvictedStatements(), lowWater(), TestLiveNoStreamWatermarkLeavesTheMarkAlone()

### Community 48 - "net.Conn"
Cohesion: 0.18
Nodes (6): relay(), interrupt(), setLowWater(), lowWaterWakes(), setLowWater(), rawSocket()

### Community 49 - "textArray"
Cohesion: 0.38
Nodes (3): TestParseTextArray(), parseTextArray(), textArray

### Community 50 - "passwordFromFile"
Cohesion: 0.20
Nodes (4): awaitExpiry(), passLineMatches(), passwordFromFile(), splitPassLine()

### Community 51 - "Error"
Cohesion: 0.22
Nodes (5): startupError(), Error, refusesProtocol(), atoi32(), versionRefusal

### Community 55 - "Hstore"
Cohesion: 0.28
Nodes (6): parseRecord(), recordField(), Hstore, hstoreString(), parseHstore(), TestParseHstoreAndRecord()

### Community 56 - "encodeAll"
Cohesion: 0.44
Nodes (9): encodeAll(), messages(), streamOf(), TestCopyStreamChangesToTextMidStream(), TestCopyStreamCountsTheRepliesItIsOwed(), TestCopyStreamEmptySource(), TestCopyStreamIsTextForATypeWithoutBinary(), TestCopyStreamReplacesAnUnusedBinaryCopy() (+1 more)

### Community 57 - "buildConfig"
Cohesion: 0.25
Nodes (3): buildConfig(), defaultHost(), parsePort()

### Community 59 - "copy_raw_test.go"
Cohesion: 0.33
Nodes (3): failingReader, failingWriter, TestLiveCopyToAndFromReader()

## Knowledge Gaps
- **2 isolated node(s):** `github.com/gsoultan/caduceus/benchmarks`, `github.com/gsoultan/caduceus`
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 150 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **18 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Conn` connect `Conn` to `Config`, `ProtocolVersion`, `restated_test.go`, `.collect`, `Conn`, `.server`, `.login`, `context.Context`, `.Wire`, `net.Conn`, `Options`, `passwordFromFile`, `.queryText`, `.WaitForNotification`, `authenticator`?**
  _High betweenness centrality (0.086) - this node is a cross-community bridge._
- **Are the 67 inferred relationships involving `liveConn()` (e.g. with `TestLiveBatchQueue()` and `TestLiveBatchQueueStopsAtTheFirstError()`) actually correct?**
  _`liveConn()` has 67 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/gsoultan/caduceus/benchmarks`, `github.com/gsoultan/caduceus` to the rest of the system?**
  _2 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `testing.T` be split into smaller, more focused modules?**
  _Cohesion score 0.06538461538461539 - nodes in this community are weakly interconnected._
- **Why does `Config` connect `Config` to `ProtocolVersion`, `recorder`, `restated_test.go`, `scan.go`, `.server`, `.login`, `context.Context`, `net.Conn`, `Conn`, `Options`, `passwordFromFile`, `Error`, `standIn`, `Connect`, `buildConfig`, `ParseConfig`?**
  _High betweenness centrality (0.077) - this node is a cross-community bridge._
- **Should `driver_test.go` be split into smaller, more focused modules?**
  _Cohesion score 0.06140350877192982 - nodes in this community are weakly interconnected._
- **Why does `Conn` connect `Conn` to `Config`, `recorder`, `restated_test.go`, `Tx`, `copyStream`, `context.Context`, `PipelineResults`, `Conn`, `TestLiveRunPreparedByName`, `.queryText`, `testing.B`, `Rows`, `statement`, `.collect`, `.server`, `.Wire`, `Row`, `.RewriteQuery`, `.WaitForNotification`?**
  _High betweenness centrality (0.061) - this node is a cross-community bridge._