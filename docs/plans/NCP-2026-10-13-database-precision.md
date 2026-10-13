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
