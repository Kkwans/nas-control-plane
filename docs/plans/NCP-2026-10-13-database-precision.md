# M14 数据库精度与结果边界交接

日期：2026-10-13。检查基线：`41c9d5c341df13a8d971e5cb4bd5c96fee96d939`，工作区已有 M13 等未提交改动，本调查未修改仓库。已阅读 `AGENTS.md`、本轮 roadmap 和 backend-audit。验证只使用临时 SQLite、纯内存 fake gRPC 和仓库外 Go overlay，不访问 NAS、真实数据库、Docker 或主机管理功能。

## 立即优先：大整数主键会误改/误删相邻行

这是实际数据正确性缺陷，建议从 M14 的 P2 提到优先交付，而不是先扩大 SQL 功能。

- `internal/httpapi/database.go:163–175`，`decodeDatabaseBody` 默认 JSON decoder 把 `map[string]any` 数字变成 float64。
- `internal/agentsocket/database_client.go:90–125`，`callDatabase` 再次 Marshal → 默认 Unmarshal → `structpb.NewStruct`，即使 HTTP 单独加 UseNumber 也会再丢精度。
- 当前 protobuf `structpb.NewValue(json.Number)` 也调用 Float64，所以“两个 decoder 都 UseNumber”仍不够。
- `internal/agentsocket/database_service.go:130–141`，`decodeDatabaseRequest` 无法恢复已经变成 double 的值。
- `internal/database/manager.go:702–735`，`mutationValues` / `primaryKeyWhere` 原样绑定收到的值；这是正确参数化，不是 SQL 注入问题，但已经错误的主键会命中错误记录。

隔离 SQLite 实证：表中同时存在 `9007199254740992` 与 `9007199254740993`。请求 JSON `keys.id=9007199254740993`，decoder 得到 `9007199254740992`；Delete 返回影响 1 行，真正删除的是相邻记录，目标记录仍然存在。实际 HTTP handler 的 decoder 也确认同样丢失。decimal token `1.234567890123456789002500` 经过当前 RPC 编码得到约 `1.2345678901234566904321`。

## M14-A：建议最小首补丁

保持现有 HTTP/RPC 结构和字符串精确值契约，不增加 typed-value envelope，不改数据库执行权限。

1. `decodeDatabaseBody`：在 Decode 前加 `decoder.UseNumber()`。保留 `DisallowUnknownFields` 和单 JSON 对象校验。Go 类型明确的 `limit/offset` 等字段仍正常解码为整数。
2. `callDatabase`：请求侧把 `json.Unmarshal(encoded, &values)` 替换为 `json.NewDecoder(bytes.NewReader(encoded))` + `UseNumber` + Decode。
3. 在 `structpb.NewStruct(values)` 前，仅遍历顶层 `values` 与 `keys` 两个 mutation 映射，把其中的 `json.Number` 换成原始 `number.String()`。不要调用 Float64，也不要 Marshal 后再默认 Unmarshal。保留已有 string / bool / nil；协议的 limit、offset 等数字不字符串化。
4. 建议抽出数据库专用 request encoder，便于直接做 codec 测试。它只属于 `database_client.go`；不要修改共享 `dashboardStruct` 或所有模块的 Struct 编码。
5. Agent 的现有 JSON 解码会原样保留这些字符串；`mutationValues` / `primaryKeyWhere` 已用参数占位符，首片无需改 SQL 拼装或表 schema。现有 Viewer 的安全整数数字也可按相同契约转成字符串参数。
6. 在 OpenAPI `DatabaseInsertRequest.values`、Update/Delete `keys` 的描述写明：整数/decimal 推荐字符串；HTTP 原始 JSON 数字 token 会精确保留为参数文本；不承诺恢复客户端在 JSON.stringify 前已经发生的 JS Number 舍入。

为什么兼容：`frontend/src/domain/database/valueConversion.ts::resolveDatabaseValue` 已对所有整数、decimal/real/float 返回字符串；`DatabaseValue` 已包含 string。`DatabaseTableView.vue::openEdit` 保存旧主键用于 WHERE，当前 UI 仍禁用主键编辑；不要趁本片放开 UI。API 本来允许在 Values 中改主键，应测试 WHERE 仍使用旧 Keys。

实验也验证过 UseNumber + 数字 token 字符串化：旧主键 `9007199254740993` 精确更新到 `9007199254740995`，相邻行不变；decimal token 写入临时 TEXT 列后原文（含末尾 002500）一致。

边界：此补丁修复“浏览器/HTTP 客户端 → Server → Agent”的传输，不让 SQLite NUMERIC 变成任意精度 decimal。SQLite 自身可能将 decimal 转为 REAL；真正 MySQL DECIMAL / PostgreSQL NUMERIC 的存储能力与边界需独立验证，当前未连接这些数据库。直接调用旧 RPC 的客户端如果已经发送 NumberValue(double)，丢失的数字不能恢复；后续可对 mutation 中不安全 double 加稳定错误拒绝。Go 直接调用方应传 string/json.Number/精确整数，不能把已舍入 float64 当成原值。

### 首片验收清单

- HTTP：实际 `decodeDatabaseBody` 得到 json.Number，分别覆盖 Values、Keys、正负 `2^53±1`、int64 最大值、uint64 最大值、长 decimal、指数、零、bool、null、原字符串。
- RPC encoder：上述数值进入 Struct 时在 `values/keys` 内是精确 string；sourceId 等字段不变，RowsRequest limit/offset 保持数字。
- RPC Agent decoder：字符串穿过 decodeDatabaseRequest 后逐字一致。最好增加纯内存 fake service capture，验证 Insert/Update/Delete 都经过相同编码器。
- 临时 SQLite：同时放相邻大整数主键；分别 insert/update/delete，确认只影响指定记录；修改主键时 VALUES 使用新值、WHERE 使用旧值。整数字符串往返，decimal 精确 token 用 TEXT/fake driver 验证，不用 SQLite REAL 冒充精确 decimal。
- 已有 safe numeric / string / bool / null 和普通小主键 CRUD 保持兼容。
- 回归 `internal/database`、`internal/agentsocket`、`internal/httpapi`；按仓库门槛做 Go 编译、前端 typecheck/build 与 OpenAPI 校验。不要把本调查的诊断 PASS 当修复测试通过。

## M14-B：SQL 结果语义，另开切片

`manager.go:233–281::Query` 与 `948–958::returnsRows` 只看首词。隔离实证：

- `-- comment\nSELECT 42`、`/* comment */ SELECT 43` 走 Exec，成功但 columns/rows 为空。
- `INSERT ... RETURNING id,label` 已插入，返回空行集，只显示 affected=1。
- `WITH x AS (SELECT 1) UPDATE ...` 走 Query，写入成功但 rowsAffected=0。

避免“Query 失败就 Exec”的 fallback，也不能先 Exec 再探测返回行：可能执行写操作两遍。不能把返回行判定当权限 allowlist。Root 任意 SQL 保持允许。

建议：先增加向后兼容的可选 `resultMode: auto|rows|exec`；缺省 auto 保留现有行为并增加可靠的前导注释识别，rows/exec 显式覆盖，原 SQL 原样送驱动。若要让 auto 正确支持 RETURNING，必须用跳过字符串/quoted identifiers/注释/括号层级的 tokenizer，处理 CTE 主语句；不要用 Contains/正则“发现 RETURNING”就认为准确。MySQL executable comments、PostgreSQL dollar quoting/嵌套注释及多语句需要明确范围和覆盖选项，不冒充完整解析器。

`database/sql.Rows` 没有通用 RowsAffected：不能从截断的返回行数推断受影响行数。保留现有字段时，新增可选 affected-count-known 元数据或驱动 adapter；未知数不要在 UI 当“影响 0 行”。`DatabaseTableView.vue:320` 当前仅在没有结果列时刷新表，所以支持 RETURNING 后还需要安排成功写 SQL 的刷新策略。

验收：带注释 SELECT、带注释 DML、INSERT/UPDATE/DELETE RETURNING、WITH SELECT、WITH UPDATE 无 RETURNING、字符串/注释内假 RETURNING、显式 mode、SQL 错误只执行一次、500 行截断后最终关闭错误。

## M14-C：总结果字节预算，另开切片

现状与实证：

- `manager.go::Query` 只限 500 行；`Rows` 只限每页 200 行；`scanValues` 对完整 []byte 复制/转 string/base64，无 cell/total 预算。
- `SELECT zeroblob(4194305)` 产生 1 个 4 MiB+1 字节单元，Query 标 truncated=false；表 Rows 同样全量返回。
- 假结果经 `databaseResponse` 生成的 protobuf 为 4,194,439 字节，超出当前 gRPC 客户端默认 4 MiB。`dialSocket` 未配置不同上限；`databaseRPCError` 把 ResourceExhausted 落成 agent_unavailable，UI 会误报 Root Agent 不可用。
- `Rows` 在检测 limit+1 探测行之前已调用 scanValues，连不返回的那一行也做了大值转换。

建议共享 result-budget helper：同时计入规范化单元、列名、行结构和元数据；设置明显小于 4 MiB 的响应预算（如 1 MiB），不能只计 len(string) 后忽略 JSON 控制字符/转义、base64 膨胀和 protobuf 结构开销。在转换大 []byte 为 string/base64 前检查原长度/编码长度，限制累计保留内存；响应 codec 再做有界防御并返回稳定 result_too_large，避免误归类 Agent 断连。不能简单把 gRPC 上限调大。

- 查询可保留完整行前缀，利用现有 truncated 并添加可选 truncationReason；不截断单元内的 bigint/decimal/主键文本。
- 表分页第一片建议超预算返回稳定错误，而不是偷偷缩短页。当前前端按固定页大小更新 offset；短页继续按 limit 跳转会漏行。要支持字节截断短页，需同时新增 nextOffset 并更新 UI，独立交付。
- 单个超大值即使只 Scan 一行，也可能已在数据库驱动内分配完整内存。通用 database/sql 的应用层限额只能保证“累计保留结果/响应”有界，不能宣称“峰值进程内存严格有界”。真正 Blob 预览/下载与驱动侧容量限制后续单独设计。
- 截断路径需要显式检查 Rows.Close 错误，再读取 Rows.Err；当前只 defer Close 且先检查 Err。固定版本 pgx/mysql 的 Close 会消费剩余协议结果并可能返回后续错误。RETURNING 等写 SQL 的大结果/取消不能包装成明确“未执行”，更不能自动重试。

验收：少于/等于/超出总预算，单 cell 超限，很多小 cell，Unicode/控制字符/base64，巨大列名，limit+1 探测行，Rows.Close 返回错误，取消释放，fake gRPC 上限与稳定错误映射，表分页不漏行。

## 已执行的诊断与证据

诊断文件全在仓库外：`/workspace/scratch/a2c31a226a28/ncp-tools/m14-probe/`，其中 `overlay.json` 临时替换三个测试文件，仅对 `-run '^TestM14Probe'` 生效。结果日志：`results.log`。不修改原测试文件，不启动 Agent 服务。

复跑命令（cwd 为 ncp）：

```sh
GOTOOLCHAIN=local \
GOMODCACHE=/workspace/scratch/a2c31a226a28/ncp-tools/go-mod \
GOCACHE=/workspace/scratch/a2c31a226a28/ncp-tools/go-cache \
../ncp-tools/go/bin/go test -overlay=../ncp-tools/m14-probe/overlay.json \
./internal/database ./internal/agentsocket ./internal/httpapi \
-run '^TestM14Probe' -count=1 -v
```

这些诊断测试用断言证明当前缺陷和可行精确参数路径，不是已经修复。没有做真实 MySQL/PostgreSQL、NAS 或部署验证。

## M14-B1 实施契约（2026-10-13 10:38 UTC）

第一片限定为显式结果模式、前导普通注释、影响行数可信度及结果收尾，不声称自动理解全部 RETURNING/CTE。auto 保留现有首关键字分类；需要返回写语句结果时用户选择 rows，需要驱动影响行数时选择 exec。分类只读取 SQL 原文；提交驱动不删注释、不改写、不拆分 SQL。

### 兼容与单次执行

- HTTP 省略 resultMode 保留旧 Query RPC。明确提交 auto/rows/exec 使用新增可选 QueryWithOptions RPC；旧 Agent 或旧 provider 返回稳定 result_mode_unsupported，必须在 SQL 执行前发生。
- 不能在旧 Query RPC 中添加字段后期待旧 Agent 拒绝：其宽松解码会忽略字段并执行。显式模式不支持时不降级重放。
- 无效模式在连接数据库之前拒绝。结果模式不是 Root SQL 权限白名单，不把 rows 声称为只读。
- 每个请求只选一个 QueryContext 或 ExecContext 路径。错误、超时、截断和关闭失败后均不自动重试，不保证 SQL 没有生效。

### 结果与界面

- 保留 rowsAffected 现有字段类型，新增 rowsAffectedKnown。驱动不支持、返回负数、跨 JSON 安全整数范围或行集路径无法取得通用计数时，界面显示“影响行数未知”，不显示伪造的零。
- rows 模式独立展示返回行数和截断；返回 500 行不代表只修改 500 行。显式关闭 Rows 并核查关闭及迭代错误，避免在关闭失败时发成功响应。
- 工作台确认冻结 SQL 和模式，重复触发不重放；成功后刷新当前表，即便 RETURNING 有列。刷新失败保留 SQL 已成功的结果。路由离开或停止等待不代表远端写入回滚。
- 结果错误应提示先核对写入状态；删除“其他数据库和表不受影响”之类对任意 Root SQL 不成立的保证。

### 新隔离证据与验收边界

临时 SQLite 诊断确认：501 行 UPDATE RETURNING 会修改全部 501 行而只展示 500 行；INSERT 后接无效语句的批次可在返回错误前持久化 INSERT；当前 SQLite 驱动的双 SELECT 批次展示最后一个结果集。不得从响应截断、失败或驱动行为推导跨数据库的事务/多结果集保证。

验收包括旧客户端/旧 Agent/旧 provider 兼容、零 SQL 调用的模式不支持结果、真实 protobuf 往返、零与未知计数、带注释查询、RETURNING 只执行一次、499/500/501 边界、Rows.Close 错误、取消资源释放及界面快照。全自动 SQL lexer、多结果集、总字节预算与真实 MySQL/PostgreSQL/NAS验证仍在本片之外。

## M14-C 实施闭环（2026-10-13）

结果预算实现为1 MiB保守累计计费与最终JSON/protobuf编码复核，保留默认4 MiB gRPC接收限额。计费包含列/键、行结构、控制字符/Unicode/HTML转义、base64和元数据；保守计费可能在编码字节数达到1 MiB之前截断。它不是数据库驱动峰值内存硬限制。

- 自定义Scanner在database/sql复制[]byte之前检查原值，计费后才转文本/base64；精确整数/小数、完整单元内容与NULL语义保留。不支持的驱动容器值和自定义编码钩子在转换前拒绝。
- Query返回完整行前缀，新增可选truncationReason为row_limit或byte_limit。首行超大时可返回零行且明确byte_limit；不等同于查询无匹配、写入未执行或回滚。Rows.Close/Err/取消仍决定最终是否可返回成功。
- 固定offset表分页超预算时整页失败；limit+1探测行不Scan。界面翻页仅在成功后提交offset，排序/页大小切换失败也不得沿旧页面继续跳过尚未显示的目标页。
- Agent在结构/自定义编码预检后再JSON/protobuf转换并复核尺寸；Server客户端在AsMap前限制protobuf节点、文本、深度及编码体积，避免小线性载荷展开为过多接口节点。稳定result_too_large不会误报Agent掉线。
- 前端分别显示行数/字节截断、完整行未能展示和整页超预算；写SQL结果错误提示先核对数据库状态，不自动重跑SQL。OpenAPI同步可选字段与错误码。

独立审查补齐匿名嵌入字段、JSON编码钩子、protobuf超多null节点及循环fake的负对照，以及排序/页大小失败后的分页状态。临时SQLite确认超大RETURNING可写入完整2 MiB值而展示空前缀，因此展示限制不限制写入范围。真实MySQL/PostgreSQL/NAS和驱动侧峰值内存仍未验证。

## M14-B1 小补丁：PostgreSQL CR 行注释（2026-10-13）

本片仅让前导行注释 helper 按驱动选择终止符：PostgreSQL 在首个 CR 或 LF 处结束，SQLite/MySQL 保持原有 LF 行为，MySQL `--` 后空白/控制字符门槛不变。分类仍只读 SQL；原文、显式 rows/exec、单次执行及错误后不重放契约未改。WITH、RETURNING、EXPLAIN、批次与块注释分类均未扩展。

官方依据（本日复核）：[PostgreSQL REL_18_STABLE scanner](https://raw.githubusercontent.com/postgres/postgres/REL_18_STABLE/src/backend/parser/scan.l)，`newline [\n\r]`、`non_newline [^\n\r]` 和 `comment ("--"{non_newline}*)` 定义（该版本文件第 195–198 行）；[SQLite 注释规范](https://www.sqlite.org/lang_comment.html) 规定 `--` 注释到 LF 或输入末尾结束。MySQL 用例只固定本项目现有行为，未验证或宣称其服务端 CR 语义。

新增纯函数独立期望覆盖 PG CR 后 SELECT/UPDATE、连续行注释、块注释、CR/LF 最早位置、LF/CRLF/EOF，以及 SQLite/MySQL 负对照、MySQL `#` 和 `--` 门槛。新增 fake runner 矩阵用 `-- comment\rSELECT 1` 独立断言省略/auto/rows 各 query=1、exec=0，显式 exec 为 query=0、exec=1；成功、预设错误、deadline 和 canceled 均逐字保留 SQL，无另一条路径或重复调用。

验证环境：`source ../ncp-tools/env.sh`，Go 1.26.4 linux/amd64。以下均通过：

```sh
PURE='^(TestLeadingQueryKeyword|TestQueryPostgreSQLCRCommentModesAndErrors|TestQueryModeChoosesOnlyOnePathAndPreservesSQL|TestQueryExecutionErrorsNeverReplay|TestQueryExecAffectedCountIsTrustworthy|TestQueryRowsBoundariesAndFinalization|TestQueryRowsFailuresCloseAndPreservePrimaryError|TestQueryDurationIncludesCloseAndCancellationCannotSucceed|TestQueryInvalidModeMakesNoCalls)$'
go test ./internal/database -run "$PURE" -count=1
go test -race ./internal/database -run "$PURE" -count=1
go test ./internal/database -run '^$'
go vet ./internal/database
git diff --check
```

测试仅执行选定的纯函数与 fake runner；未运行 Manager、sql.Open/sql.OpenDB 夹具、集成监听器，也未连接实际 SQLite/MySQL/PostgreSQL、NAS 或部署环境。编译检查包含包内全部测试源码，但不执行其他测试。独立审查复核实现及上述纯测试，无阻塞发现；这不是全套数据库集成验证。

## 自动分类范围复核结论（2026-10-13）

继续保留显式rows/exec与既有auto契约，不引入通用RETURNING token分类器。PostgreSQL官方[grammar](https://raw.githubusercontent.com/postgres/postgres/REL_18_STABLE/src/backend/parser/gram.y)允许AS ColLabel及attr_name→ColLabel，[关键词规则](https://www.postgresql.org/docs/18/sql-keywords-appendix.html)也允许保留字作为列标签；例如SELECT项别名returning或t.returning属性引用不等于返回子句。不能仅靠顶层token出现就改变执行路径。

当前UI与OpenAPI已说明RETURNING/CTE应显式选择结果模式；本次未确认新的实现缺陷，不增加未实现的窄语法待办。独立在53962e4源码上复跑九项既有纯函数/fake-runner query测试通过，未连接真实数据库或NAS。完整自动分类仍为保留范围，不能标记为已完成。

## M14-B2：普通 WITH 主写语句影响计数（2026-10-14）

现有显式 rows/exec 与只执行一次的协议保持。auto 仍把全部 WITH 走 Query，普通无 RETURNING 的主写语句因此显示影响数未知。临时 SQLite 已验证普通更新、零匹配、多 CTE、递归、materialization 以及字符串假 RETURNING 均可由 Exec 得到正确计数；n=n+1只执行一次。

本片只为 SQLite/PostgreSQL 提供保守的负向证明：完整匹配 WITH [RECURSIVE] name [(names)] AS [NOT MATERIALIZED|MATERIALIZED] (非空body) [, ...] 后，主语句仅为 INSERT/UPDATE/DELETE，且主语句顶层没有裸 RETURNING token，才选择 Exec。任何顶层 RETURNING（包括别名或属性名）一律保留旧 Query；CTE body/子表达式/引号/普通注释中的同名文本不参与该否定判断。直接 DML RETURNING、MERGE、REPLACE、SEARCH/CYCLE 与完整 auto 分类未完成，不扩张宣称。

扫描器要求单调游标、流式token、无递归/全文token数组/全文大写。新扫描限1MiB、括号与PG块注释深度256；超限或未知均回退旧行为，不拒绝Root SQL。支持普通ASCII词/数字、单双引号双写转义、有限运算符；SQLite另外支持反引号与方括号，PG块注释可嵌套、行注释CR/LF，SQLite非嵌套块注释和LF行注释。PG dollar、特殊字符串前缀(E/B/X/N/U&)、普通字符串反斜杠、未知标点，以及SQLite参数起始符均先保守回退；执行注释也回退。只接受单语句，最多一个顶层尾分号及普通尾注释；未闭合、内部裸分号、第二分号或尾随实质token均回退。MySQL路径不变；既有前导扫描不受新1MiB限制，不能宣称全auto CPU上限。

集成仅在returnsRows的WITH分支增加纯helper；原SQL逐字传现有runner、仅调用一次。任何错误、取消、计数不可用、Close或预算失败都不换路重放。分类不证明只读、SQL有效或权限；PG影响计数仅代表主语句而非CTE内部所有写入。

验收：纯分类覆盖支持/回退/大小与深度临界及随机输入；独立期望的fake runner覆盖省略/auto/rows/exec、原SQL与单次调用；临时SQLite覆盖三类主DML零/非零计数、n=n+1及WITH RETURNING保持结果。PG只纯函数/fake，无真实连接；同步UI说明与OpenAPI文字，不改模型/RPC/预算。明确保留完整RETURNING自动识别的后续边界。

依据：[PostgreSQL WITH](https://www.postgresql.org/docs/18/queries-with.html)、[词法](https://www.postgresql.org/docs/18/sql-syntax-lexical.html)、[官方grammar](https://raw.githubusercontent.com/postgres/postgres/REL_18_STABLE/src/backend/parser/gram.y)、[SQLite WITH](https://www.sqlite.org/lang_with.html)、[RETURNING](https://www.sqlite.org/lang_returning.html)、[注释](https://www.sqlite.org/lang_comment.html)、[参数](https://www.sqlite.org/lang_expr.html)、[MySQL执行注释](https://dev.mysql.com/doc/refman/8.4/en/comments.html)。这些范围按实际引擎差异保守收敛，遇不确定直接保留旧路径。

### M14-B2 发布前验证

上述五个数据库文件已实现并冻结：新增流式scanner和独立期望测试，returnsRows仅新增WITH分支；executeQuery/预算/收尾不改。完整internal/database测试及race、vet通过；5秒fuzz共30,236次通过。独立对抗矩阵涵盖四模式/四结果、1MiB与256边界、PG续段/三引号/词边界、NUL与CR后语句，另以84条临时SQLite的实际返回列作为独立oracle，定向race全部通过，无阻塞发现。仅临时SQLite/fake，不宣称PG/MySQL实机。

主任务精确快照另通过internal/database、完整OpenAPI、全仓Go build/vet；UI说明与OpenAPI同步有限自动识别范围和不重放原则。前端完整验证及发布后CI继续跟进；完整直接RETURNING自动识别仍未完成。

精确前端验收（02:07 UTC）：2410单元/106文件、全量lint、Vue类型、生产构建、dist权限与540KiB预算全部通过，最大chunk518.0KiB。主任务已审查全部精确diff；未改执行权限、RPC协议、生产连接或凭据。

## M14-B3：SQLite 直接 DML RETURNING（2026-10-14）

新证据允许把正向识别范围限于SQLite，不沿用此前被否定的跨驱动关键字判断：仓库固定SQLite 3.53.3的[parse.y](https://raw.githubusercontent.com/sqlite/sqlite/version-3.53.3/src/parse.y)中RETURNING不属于标识符或ID fallback，四个对应产生式均建立返回子句；ANY通配仅用于CREATE VIRTUAL TABLE参数，不能从直接DML进入。PostgreSQL的列标签/属性歧义仍存在，不扩展其auto。

技术方案：仅首词INSERT/UPDATE/DELETE且driver=sqlite时复用cteScanner，完整扫描确认单条语句及顶层裸RETURNING、有后续返回表达式标记。最大1MiB SQL和256嵌套深度，线性扫描不积累全token数组；任何未知词法、不闭合、批次、重复顶层RETURNING或预算超限保留原exec路径，不拒绝原SQL。REPLACE、WITH、PG/MySQL及显式模式原路不变。不进行SQL语法验证或改写，派发始终一次，失败不切换重放。

已知保守范围：十六进制数、下划线数字、未引用Unicode标识符及SQLite允许的未结束EOF块注释可仍走旧exec；需要返回行时显式rows可执行，不能宣称完整RETURNING识别。SELECT/EXPLAIN等既有行为不变。

[SQLite官方RETURNING说明](https://www.sqlite.org/lang_returning.html)指出引擎在发出返回行前会缓存输出，返回次序不保证，且不包含AFTER触发器后的值。因此面板500行/1MiB预算只限制保留的展示数据，不限制SQLite内部临时内存、写入总数或事务范围；不把输出行数当作驱动影响行数。

隔离验证：临时SQLite三种DML及auto/省略模式确认写一次并展示返回值，零匹配保留列和未知影响数；499/500/501行更新及首行2MiB字节截断都在Rows.Close后核验完整写入。fake执行器覆盖错误/取消/扫描/关闭不重放。独立56项额外SQLite语法oracle与960组模式/驱动/错误路径通过race；完整database测试/race、5秒23412次fuzz通过。主任务精确快照database/OpenAPI测试和全仓Go build/vet通过；无真实PG/MySQL或NAS运行。

主任务前端精确验收（05:31 UTC）：3013单元/113文件、全量lint、Vue类型、生产构建、权限与540KiB预算全部通过。首轮一项旧提示文案断言失败，恢复“展示结果”措辞后全量复验通过，未修改断言。UI与OpenAPI同步SQLite有限识别范围；实际后继CI继续核验。
