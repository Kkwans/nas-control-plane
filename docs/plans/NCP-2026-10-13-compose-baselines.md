# M07-B 实施交接：磁盘编辑基线与部署成功后的目标核对

日期：2026-10-13。审查基线：`76a461c376c702e7b30d5dd18627ccf943a93235`，包含 M07-A `540e532`。已读 `AGENTS.md`、原 Compose handoff 的 B 节、当前 Manager/RPC/HTTP/UI 与测试；本文件在仓库外。审查期间其他工作将 HEAD 推进到 `f9c3fc3`，Compose 所述代码没有变化。本次没有修改仓库、提交、运行测试、启动 Agent、调用 Docker 或接触 NAS；下述检查是实施验收要求，不是已通过结果。

## 1. 决策与交付边界

本片完整贯穿 **磁盘读取 hash → UI 冻结基线 → HTTP/RPC 强制传递 → Manager 两次写前核对 → 命令成功后再核对 → UI 窄范围推进基线**。

1. 必填 `baseContentHash`，不保留“缺省时照常部署”。格式固定为原始文件字节的 SHA-256、64 位小写 hex。
2. 新增独立 RPC `DeployConfigChecked`，使用可选的 Go client/server 接口；新 Server 永远调用该方法，绝不退回旧 `DeployConfig`。只添加 Struct 字段不安全：旧 Agent 会忽略未知字段然后执行覆盖。
3. Manager 同 target 锁内，在校验前核对一次，临时文件写完且最终 rename 前核对一次同一个基线；任何确认冲突均不覆盖目标、不调用部署命令。
4. 部署命令返回成功后，如果目标已变或无法核实，返回确定的“部署确认失败”业务码，`Completed=false`；不自动恢复，不记录成功 revision。
5. 不新增 Job 结果 schema。Job 完成后 UI 用现有只读 API 获取目标快照，只有 **该目标返回内容严格等于冻结提交内容，且服务器 hash 合法** 时，推进到该 hash。推进与当前编辑器是否已继续编辑无关；文本、草稿保存基线都保持原样。无需 `crypto.subtle` 或新的浏览器 hash 依赖。
6. 保留 M07-A 的有界独立恢复、条件写回、细分错误与传输结果未知语义。不要同时重做 Job、数据库、图表或整套 protobuf。

这里的完成定义是：部署命令返回成功，随后一次读取确认目标字节与提交相同。不是容器健康证明，不是跨进程事务，也不保证此后文件持续不变。

## 2. 当前代码的具体缺口

- `internal/compose/manager.go:20–25,53–59`：`FileSnapshot` 无 hash，`DeployRequest` 无基线；`Read` 的 `size` 使用读取前 stat 值。
- `manager.go:103–143`：锁内先 validate、再读原文、备份、无条件 `atomicWrite`。外改无论发生在用户打开文件以后还是校验期间，都可能被覆盖。
- `manager.go:167`：runner 返回 nil 就 `Completed=true`，没有核对实际目标。
- `manager.go:390` 起：已有 `atomicWriteGuarded`，回调位于临时文件写入、Sync、Close 之后，最终 Rename 之前；适合复用。现有 `verifyCurrentContent` 的错误码专属于回滚，不能直接原样用于发布前/后，否则文案会误称回滚。
- `internal/agentsocket/compose_rpc.go`：手写服务和 `structpb.Struct` 载荷。`compose_service.go:67–110` 只取已知字段，旧二进制会静默丢弃新增基线。
- `internal/agentsocket/client.go:511–539`：现有客户端直接调用 `DeployConfig`；`rpcError` 对全局 Unimplemented 映射为 `AGENT_PROTOCOL_MISMATCH`。
- `internal/httpapi/compose.go:87–125`：HTTP 未校验基线就建 Job；`Completed=true` 后记录提交内容为成功 revision。
- `frontend/src/composables/useComposeWorkspace.ts`：`load` 已先保存磁盘内容再覆盖 draft；`publish` 已冻结提交内容。成功后却无条件更新 `baseContent`，没有服务端 hash，也没有目标复读。
- `frontend/src/api/docker.ts:733–740`：运行时 `isComposeProjectConfig` 没有验证 hash；只改 TypeScript interface 不够。

现有 `internal/agentsocket/database_rpc.go` 的 `QueryWithOptions` 可选接口、`database_client.go` 的 no-fallback 调用，以及 `database_query_test.go` 的旧服务描述/bufconn 测试可作为实现模式参考。只借鉴，不修改这些其他工作拥有的文件。

## 3. 数据和错误契约

### 3.1 精确字段

- Go `FileSnapshot.ContentHash string`，JSON `contentHash`，必填响应字段。
- Go `DeployRequest.BaseContentHash string`，JSON `baseContentHash`，必填请求字段。
- Compose 手写 RPC request 继续使用现有 snake_case，字段名 **`base_content_hash`**。response 经 `dashboardStruct` 继续使用 JSON camelCase **`contentHash`**。这两处不要混淆。
- TypeScript `ComposeFileSnapshot.contentHash: string`、`ComposeDeployInput.baseContentHash: string` 都是 required。
- OpenAPI 在对应 required 数组加入字段，schema 为 `type: string`、`pattern: '^[0-9a-f]{64}$'`、`minLength: 64`、`maxLength: 64`。说明 hash 来自本次编辑会话读取的目标磁盘内容，不是 draft/revision/提交内容 hash。
- 不改 `ComposeDraft.contentHash` 或 `ComposeRevision.contentHash` 的已有语义。它们不能充当部署磁盘基线。
- `DeployResult` 不需要新增 hash，Job/SQLite 不需要迁移。本片选用成功后目标复读路径完成 UI 基线推进。

必须拒绝：缺字段、空字符串、非字符串、前后空白、非 hex、长度错误、大小写不符合 canonical 格式。不能 TrimSpace、猜测缺省、自动把 hash 替换为当前值，也不能由 HTTP 临时读磁盘来替旧客户端“补齐”。64 个零形式合法，但通常会在内容核对时冲突。

### 3.2 稳定错误码

沿用全部 M07-A 代码，新增以下有限代码；公开提示只由稳定代码产生，不带原始配置、stderr 或底层 err 文本：

| 代码 | 事实与处理 | 建议公开提示 |
|---|---|---|
| `COMPOSE_BASELINE_INVALID` | 缺少/无效编辑基线；未执行部署 | 缺少有效的 Compose 编辑基线，请重新读取配置后再发布。 |
| `COMPOSE_BASELINE_UNSUPPORTED` | 可选 checked 接口不存在或该 RPC 方法 Unimplemented；未调用旧方法 | 当前 Agent 不支持安全的编辑基线检查，未执行部署；请更新 Agent 后重新读取。 |
| `COMPOSE_CONFIG_CONFLICT` | 写入前的内容、存在性或普通文件类型与预期不符；未写目标、未运行 Deploy | Compose 配置已发生变化，未写入或部署；请保留编辑内容，重新读取并比较。 |
| `COMPOSE_CONFIG_UNVERIFIED` | 写入前无法读取/核实目标；未写目标、未运行 Deploy | 无法核实当前 Compose 配置，未写入或部署；请检查文件并重新读取。 |
| `COMPOSE_DEPLOY_POSTCHECK_CONFLICT` | 部署命令成功，但后续核对发现目标变化；不恢复、不记成功版本 | Compose 应用命令已完成，但目标配置随后发生变化；未自动覆盖或保存成功版本，请核对配置和运行状态。 |
| `COMPOSE_DEPLOY_POSTCHECK_UNVERIFIED` | 部署命令成功，但后续核对无法确认目标；不恢复、不记成功版本 | Compose 应用命令已完成，但无法核实目标配置；未保存成功版本，请核对配置和运行状态。 |

`UNVERIFIED` 表示无法确认，不应改写成“确认被外部修改”。`POSTCHECK_*` 表示命令已经运行，不能说“部署未执行”。这些为 Job `failed`，并不暗示运行态没有变化。超时、取消、断线、无法解码完成响应仍按已有 `interrupted`，不改成上述确定业务结果。

## 4. Manager：一次原文捕获、两次写前核对、一次成功后核对

### 4.1 读取快照

`Read` 对实际读到的同一份 `[]byte` 计算 hash、生成 content，`Size=int64(len(contentBytes))`，而不是 `info.Size()`。保留 2 MiB 校验，至少在读取后也检查实际长度；不要扩大成完整文件系统重构。

文本传输必须可逆：对非法 UTF-8 文件拒绝返回可编辑快照，沿用读取失败错误路径即可。Go JSON/Struct 的替换字符会使“返回字符串相等”不再等同原始字节相等，因此不应一边替换一边提供原字节 hash。合法 UTF-8 的 BOM、CRLF、尾随换行、空白、中文/emoji 均原样保留；不用 YAML 规范化、TrimSpace、换行转换或 Unicode normalization。hash 仍只对原始字节计算。

在 Server 的 `ReadComposeConfig` 响应边界也校验每个 `contentHash` 的格式，并对返回 `content` 的 UTF-8 字节重算 SHA-256 核对；缺失/不一致按 `AGENT_RPC_RESPONSE_INVALID` 拒绝，不为旧响应补 hash。这样 UI 使用的是已核验的服务端内容/hash 对，不只是形状像 hash 的字符串。此处只有 Go 标准库计算，不引入浏览器加密依赖。

测试可把文件读取/快照构造提成很窄的包内 helper，直接使用 `t.TempDir`。不得为测 `Read` 绕开生产路径限制，或创建真实 `/volume1`、`/volume2`。

### 4.2 明确的执行顺序

公共 `Deploy` 保留路径和 target 属于 configFiles 的校验。执行 helper `deploy` 本身也要拒绝无效 hash，不能让包内测试/调用绕过必填语义。

1. 校验 hash 格式并 decode 为 `[32]byte`；获取现有规范化 target 锁，保持到所有后续处理完成。
2. 锁内、`runner.Validate` 之前读取当前普通文件的内容与必要模式信息；缺失/非普通文件为 `COMPOSE_CONFIG_CONFLICT`，无法读取为 `COMPOSE_CONFIG_UNVERIFIED`。对这一份捕获字节核对 expected hash。
3. 该捕获字节就是 `original`。只在第一次核对成功后 validate 提交内容，随后备份 `original`。不要 validate 后再次读取一个新 `original` 并悄悄把它当作用户批准的基线。
4. 保留现有唯一 0600 备份和模式处理。创建备份失败就终止；第二次核对发现冲突时，可能已经有一份备份，保留它是允许的。“未改文件”专指目标文件，不应声称完全没有文件系统副作用。
5. 用 `atomicWriteGuarded(target, submitted, mode, beforeRename)`，在 callback 内先看 `ctx.Err()`，再读取目标、核对 **同一个 expected baseline hash**。callback 在临时文件已写完/Sync/Close 后且 Rename 前。读取失败/不匹配按写前代码终止；临时文件必须清理，不运行 `runner.Deploy`，不进入恢复分支。
6. 只有 Rename 成功后才调用一次 `runner.Deploy`。若其失败，继续 M07-A 现有恢复路径：恢复仅在当前目标等于本次 `submittedHash` 时进行；原始 deployErr、独立 rollback context、errors.Is、细分恢复码都保留。
7. 若 runner 返回成功，仍在同一 target 锁内核对当前普通文件的字节是否等于 `submittedHash`。不同/缺失/非普通文件返回 `COMPOSE_DEPLOY_POSTCHECK_CONFLICT`；读取无法确认返回 `COMPOSE_DEPLOY_POSTCHECK_UNVERIFIED`。保留 `BackupPath`，保持 `Completed=false`。**这里绝不进入 rollback/reapply。**
8. 只有成功后核对通过且当前调用未取消，才设置 `result.Completed=true`。若当前 context 已结束，返回取消/超时，沿用结果未知语义；不能用已结束的调用确证远端终态。

建议把当前核对函数内部的“内容/存在性/类型冲突”和“读取无法确认”变成阶段无关的窄 helper 或 sentinel，再由 prewrite/postcheck/rollback 包装为各自代码。保留旧 rollback wrapper 与测试的等价行为。不要复制三套稍有不同的 hash 逻辑，也不要通过 error 字符串转换阶段。

`Lstat` 检查的普通文件规则与已有恢复保护保持一致。本片应明确目标为 symlink 时拒绝受保护部署，不尝试跟随后再替换它；父目录 symlink/别名、inode、权限、所有者和多文件一致性并未因此得到保护。

## 5. RPC：旧 Agent 必须在执行前停止

### 5.1 可选接口与独立方法

在 `compose_rpc.go` 增加 full method：

`/ncp.agent.v1.AgentComposeService/DeployConfigChecked`

保留现有 `AgentComposeServiceClient` / `AgentComposeServiceServer` 必需方法集合不变，新增：

- `AgentComposeCheckedServiceClient`：嵌入旧 client 接口，加 `DeployConfigChecked(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)`。
- `AgentComposeCheckedServiceServer`：嵌入旧 server 接口，加 `DeployConfigChecked(context.Context, *structpb.Struct) (*structpb.Struct, error)`。
- concrete `agentComposeServiceClient` 实现新方法，constructor 的旧返回类型保持兼容。可额外提供 checked constructor，但不必因此改所有调用点。
- ServiceDesc 增加新方法，handler 按可选 server 接口做 type assertion；缺少时返回 Unimplemented，不 panic、不调用旧方法。保留拦截器与新 FullMethod。
- `composeService.DeployConfigChecked` 在调用 provider 前验证全部必填字段及严格 hash。调用仍传类型化 `ncpcompose.DeployRequest`，不是增加通用 exec。

这里的“typed optional”是静态 Go 接口分派；线上的载荷仍是仓库既有 Struct，不宣称升级成 schema-first protobuf。无需反射查方法、版本字符串探测或泛化能力协商。

### 5.2 Provider 和旧入口

`ComposeProvider.Deploy` 的 Go 签名已经接收 `DeployRequest`，不必增加 provider 接口或重复新 Manager 方法。生产 provider 是同时编译的新 Manager，必须强制 hash。

旧 `composeService.DeployConfig` 仍存在，但 **也走同一必填 hash 校验及受保护实现**。旧请求通常没有 hash，返回 `COMPOSE_BASELINE_INVALID`，provider 调用计数为 0；绝不能保留无条件覆盖分支。携带合法 hash 的直接旧入口请求可以受保护地运行一次，但新 Server 不使用此入口，不能以此兼容措施在旧 Agent 上试探执行。

### 5.3 Server client 调用规则

`DeployComposeConfigWithClient`：

1. 先处理已取消 context，再验证 request hash；空基线时不调用 RPC。
2. 断言 client 实现 `AgentComposeCheckedServiceClient`；否则返回 typed `COMPOSE_BASELINE_UNSUPPORTED`，旧 DeployConfig 调用数必须为 0。
3. 序列化 `base_content_hash`，仅调用一次 checked RPC。
4. 只在这一调用的 Unimplemented 情况下，将错误转换为 typed `COMPOSE_BASELINE_UNSUPPORTED`。保持 causes/`errors.Is`，不要全局改变 `rpcError(Unimplemented)`，不要依赖错误 message 中出现代码，也不要发起旧方法重试。
5. context Canceled/DeadlineExceeded 优先；其他 Unavailable、EOF、未知业务码按已有结果未知处理。仅缺 optional/Unimplemented 是此处的执行前协议拒绝。
6. 成功响应继续校验 Completed、ProjectID、BackupPath；应补 target/project 与冻结 request 的一致性校验，至少不接受空/错误 target。当请求允许相对路径时，应按既有规范化规则比较预期 target，不要无意拒绝合法规范化结果。HTTP/UI 现有发现路径通常是绝对规范路径。错误完成响应不得被当作部署未执行，按 `AGENT_RPC_RESPONSE_INVALID` 保留未知语义。

`composeDeployFailure` / `IsDeployErrorCode` 加上新增有限代码，service 只白名单转发。可以沿用当前 Internal/InvalidArgument 映射，避免为了语义更漂亮扩大全局映射；方法 handler 缺能力才使用 Unimplemented。原 `rpcError` 对 Internal/InvalidArgument/Aborted 的白名单识别已可承接新增业务码。

### 5.4 兼容矩阵

| 组合 | 确定行为 |
|---|---|
| 新 UI → 新 Server → 新 Agent | read 得到磁盘 hash，checked RPC 执行两次写前和一次写后核对 |
| 旧 UI/旧 HTTP 请求 → 新 Server | HTTP 400 `COMPOSE_BASELINE_INVALID`，没有创建 Job、没有 Agent 调用 |
| 新 Server → 旧 Agent（只有旧 ServiceDesc） | checked RPC Unimplemented；Job failed `COMPOSE_BASELINE_UNSUPPORTED`，旧 DeployConfig 从未运行 |
| 新 Server → 注册了新描述但未实现可选 server 的 Agent/test double | 新 handler 返回 Unimplemented；同上且不 panic |
| 新 client helper → 仅旧 client 接口的 test double | 本地 typed unsupported，任何 deploy 方法均未调用 |
| 旧 Server → 新 Agent | 旧 RPC 缺 hash 在 provider 前拒绝；旧 Server 可能呈现通用/未知错误，但不会部署 |
| 新 UI → 旧 Server/Agent 的读响应缺 hash | 运行时快照校验拒绝，不构造可发布基线；提示更新/重新读取，不计算替代 hash |
| 旧 UI → 旧 Server → 旧 Agent | 旧版本保持旧风险；本片不能约束尚未更新的整条链路 |

上线需要 Agent/Server/UI 配套更新。混用版本宁可暂时阻止发布，也不能静默降级。这里只明确升级行为，不执行任何生产升级。

## 6. HTTP、Job、revision

`internal/httpapi/compose.go` 解码后、`jobs.submit` 前验证 `baseContentHash`，无效则 HTTP 400 `COMPOSE_BASELINE_INVALID`。已有全局 body/schema 解码错误继续正常 400；不能先建 Job 再拒绝可本地确认的缺基线。

Job 执行中的新增六个确定代码使用现有 `jobs.fail`，`ErrorCode` 和安全 `message/error` 一致并持久化。`COMPOSE_CONFIG_CONFLICT` 是已接受 Job 的终态失败，不是同一 POST 的事后 HTTP 409。OpenAPI 要写清 HTTP 400 与异步业务失败的区别。

复用 `IsDeployErrorCode` 让 `jobRPCOutcomeUnknown` 识别新代码；无需修改 `job_engine.go` 或新建数据库字段。只有 checked RPC 返回可信 `Completed=true`，且调用未被取消，才进入 `RecordComposeRevision` 和 completed 路径。所有 precheck/postcheck/unsupported、RPC 异常、Completed=false 情况的成功 revision 调用数必须为 0。

成功文案建议收窄为“Compose 应用命令已完成，已核对目标配置并保存版本”；记录失败仍只说记录失败，不把已完成命令改称健康失败。`controlStore=nil` 时不要说“已保存版本”。这些是当前同一分支的小修正，不扩大成健康验收。

## 7. UI：磁盘基线、草稿保存基线各守原语义

### 7.1 Load 和冻结 payload

- 新增 `baseContentHash: Record<string, string>` 或同义独立 map；reset 清空。
- `load` 收到读取响应时，立即从尚未覆盖 draft 的 snapshots 建立磁盘内容基线和 hash map，再套用已保存 draft。`savedContent` 继续表示 NCP 草稿保存基线。
- `isComposeProjectConfig` 要求每个文件 hash 合法，不可依赖 TS 类型。UI publish 也在冻结时确保目标 hash 存在且合法；没有就提示重新读取，不提交。
- 正常发布与历史版本发布都使用当前目标的 `baseContentHash[targetPath]`。历史 revision 的 hash 只描述历史内容，不能用于检查当前磁盘；保存草稿/loadRevision 也不能更新磁盘 hash。
- `payload` 在 validate/confirm 前冻结 `baseContentHash` 与 content/path/project/configFiles。确认时、任务运行时继续编辑，不改变 payload。原有 validation revision、workspace/session gate、重复点击锁、close observation 规则不变。
- Job 失败可保留 `completed.errorCode` 构建 NcpApiError，避免仍硬编码 `COMPOSE_DEPLOY_FAILED`；文本继续优先可信安全详情。

### 7.2 成功后的唯一允许推进

收到 `completed.status==='completed'` 后先不要修改 `baseContent`/hash。保持 `deploying=true` 直到窄复读结束，避免下一次提交与基线推进重叠。

用现有 `readComposeConfig`，请求绑定被冻结的 project id、workingDirectory；可只传 `configFiles: [payload.targetPath]`，避免其他无关文件读取失败。若沿用所有 configFiles，则仅选择目标快照，绝不整体 `load()`。

在 await 前后检查原 workspace/session `owns()`。取返回 `path === payload.targetPath` 的唯一文件并验证项目身份、合法 hash；missing/duplicate/不匹配均不推进。只能在：

`targetSnapshot.content === payload.content`

成立时，更新：

- `baseContent[payload.targetPath] = payload.content`
- `baseContentHash[payload.targetPath] = targetSnapshot.contentHash`

不得把比较条件写成 `targetSnapshot.content === activeContent.value`，也不得要求当前 editor 内容仍等于提交内容。例：基线 O，提交 A，确认后编辑成 B，磁盘复读 A；应推进基线到 hash(A)，编辑器仍 B，dirty 仍依据已有 savedContent 保持 true。下一次发布 B 必须带 hash(A)。如果只在 editor 未变时推进，会让正常后续编辑永远持有过时 hash(O)。

任何情况下都不在这一复读里改 `files[*].content`、`savedContent`、`draftUpdatedAt`，不读取并覆盖其他文件、不套 draft、不切 activePath。发布历史版本也是同样规则。

### 7.3 两类失败窗口要分开呈现

- Agent 成功后检查就发现外改：Job failed `POSTCHECK_*`，无成功 revision；UI 不做成功推进、不显示成功提示。
- Agent 确认完成之后，UI 复读才发现 external 或读取失败：Job 的已确认历史结果不被反向改成失败，但 UI 不推进 hash、不替换文本、不把 external 设为新基线。显示“任务已完成，但目标配置已变化/无法重新核实；已保留编辑内容，请重新读取并比较”。可以正常刷新历史列表，但不要把其成功覆盖掉这个警告。

版本记录刷新错误与磁盘复读警告独立处理，保留优先的配置核对警告；不要由后续泛化异常把完成任务误报为“部署失败”。失败/冲突下用户已有文本留在当前工作区，不自动 reload 或 rebase。现有“重新读取”会 reset 编辑器，因此提示需要先保留文本，并沿用已有用户发起的读取流程；本片不添加自动合并/丢弃行为。

本片不依赖 `window.crypto` 或安全上下文。服务器对有效 UTF-8 原始字节生成 hash；UI 只做规范字符串检查和严格内容比较。不能从 `size`、时间戳、历史 revision、draft hash 或此刻 editor 计算/猜测推进值。

## 8. 精确文件所有权

本片实施者可拥有以下 Compose 相关改动：

- `internal/compose/manager.go`、`manager_test.go`，必要时新增同包 `content_baseline.go`/`content_baseline_test.go` 作窄 helper。
- `internal/agentsocket/compose_rpc.go`、`compose_service.go`、`compose_service_test.go`；可新增 `compose_baseline_test.go` 以隔离新 bufconn 场景。
- `internal/agentsocket/client.go` **仅 Compose read/deploy 边界**。`rpcError` 只在确有白名单映射需要时改，其他域语义不能动。
- `internal/httpapi/compose.go`、`compose_deploy_test.go`；仅必要时在 `docker_images.go` 的共享结果分类增加窄代码分支（若使用现有 `IsDeployErrorCode`，无需改此文件）。
- `frontend/src/api/system.ts` **仅 Compose DTO**；`frontend/src/api/docker.ts` **仅 Compose 校验/调用**；`frontend/src/api/docker.test.ts` 的对应测试。
- `frontend/src/composables/useComposeWorkspace.ts`、`useComposeWorkspace.test.ts`。
- `api/openapi/openapi.yaml` **仅 Compose endpoints/schemas**，以及必要的 contract test。该文件与 M14 发布共享，开始修改前由 root 协调，严禁覆盖数据库 schema/hunks。
- 少量现有 Compose fixture 需补 hash，先查找再定点修改；不是全局放宽 runtime validator。对属于其他 worker 的文件先向 root 说明。

不拥有 `database_*`、监控图表/selector/e2e 修正、M14 发布、Job schema、controlstore migrations、部署文件、Release/version 或路线图总表。不要改他人的脏文件、格式化整个共享文件或提交其他 worker 的改动。当前 `docs/qa/NCP-2026-10-13-history-layout.md` 是其他工作，不纳入本片。

## 9. 隔离、确定性验收

全部使用 fake runner/provider、`t.TempDir`、临时 SQLite、`httptest` 和 gRPC `bufconn`。禁止真实 Docker、NAS、root 服务或真实 Unix socket；异步测试使用 channel/deferred/synctest，不用 sleep 猜测时序。

### 9.1 Manager/文件系统

1. snapshot 的 hash 对原始字节、Size 对实际长度；含 CRLF、BOM、中文/emoji、末尾空白案例；非法 UTF-8 不生成可发布快照。
2. 缺/坏 hash 直接拒绝，Validate/Deploy/写目标/备份计数均为 0。
3. 用户读 O 后另一次部署变 B，再提交携 hash(O) 的 A：第一次核对冲突，无 Validate/Deploy、目标 B 保留，无新备份。
4. 相同 YAML 语义但注释/空白/换行不同仍冲突；不做规范化比较。
5. 第一次核对通过后 fake Validate 用 barrier 让外部写 E，再返回成功：第二次核对拒绝，E 保留，Deploy 0 次；已有备份内容仍为 O，临时文件清理。
6. 直接 helper 测试证明 prewrite callback 在临时文件完成后、Rename 前；在 callback 修改目标，Rename 不发生。不要只测“调用 atomicWrite 前检查”。
7. 缺失、目录、symlink、读取不可确认：区分 CONFLICT/UNVERIFIED，目标不重建/覆盖；用注入读取错误测权限失败，不依赖测试进程 uid 或 chmod 实际效果。
8. fake Deploy 读到 A、等待 barrier，测试写 E/删除/换目录，然后 runner 返回 nil：Completed false、POSTCHECK_CONFLICT、没有第二次 Deploy/没有恢复写回、保留 E/缺失/目录与备份 O。
9. runner nil 后读取错误：POSTCHECK_UNVERIFIED，Completed false、无 reapply；只能通过窄 helper/reader seam 模拟，不引入全套 FS mock。
10. 无外改完整成功：目标 A、Completed true、备份 O、模式与临时清理正确。
11. 两个请求都携 hash(O)，同 target 排队：第一个成功 A 后，第二个拿到锁必须在 Validate 前冲突；不能把排队当成自动获准覆盖 A。
12. 合法连续两次部署须显式让第二个请求基线变 hash(A)。更新现有“同秒备份”等重复部署测试，不能为了旧 fixture 放宽新必填规则。
13. 全部 M07-A：部署失败外改保持、恢复不可确认/写失败/reapply成功或失败、独立 timeout、errors.Is、同 target 锁覆盖恢复、不同 target 并行和取消等待保持。

### 9.2 RPC/bufconn

1. 正确的 `base_content_hash` 原样经过 client → checked RPC → provider；response contentHash 同样穿过 read。read 响应缺 hash、格式错、hash 与 content 不一致都拒绝，不能补算后接受旧响应。
2. missing/invalid field 在 service 层 provider 调用为 0；直接旧 DeployConfig 无 hash 也为 0。
3. 真正“旧描述没有新方法”的 bufconn server，仅旧 DeployConfig 会增加计数；新 client 返回 BASELINE_UNSUPPORTED，计数必须 0。
4. 新 ServiceDesc + 仅旧 server 接口：可注册且返回 unsupported，无 panic、旧方法 0。
5. 仅旧 client optional assertion 失败：任何 RPC 0；checked method 返回 Unimplemented：checked 1、legacy 0。禁止 fallback/replay。
6. 新增错误码与 M07-A 代码穿透正确，未知/带敏感后缀错误仍被过滤；公开错误不含 fixture secret。
7. 超时/取消覆盖业务文本分类；新 checked RPC 被阻塞超过 deadline 的 bufconn 测试保持未知，即使服务稍后返回恢复/成功信息。
8. malformed/空/错误 target 完成响应不会产生可信成功；有效 completed response 只对正确 request 通过。

旧 Agent 不受保护的断言必须用旧 descriptor 或旧接口假服务复现；只 mock 一个普通错误不足以证明旧方法没有执行。

### 9.3 HTTP/Job/存储

1. 空/缺/格式不对的 baseContentHash → HTTP400 BASELINE_INVALID；jobs.submit/provider/revision 调用 0。
2. 含合法基线的请求原样到 fake Compose client；未知 hash 不能由 HTTP 自行刷新。
3. 六种新增代码与原 M07-A 终态都变 failed、ErrorCode/安全文案持久化且 reload 不丢失；revision 调用 0。
4. postcheck 失败即使 fake result 误带 Completed=true，也以 error 优先而不存 revision。
5. nil error 但 Completed=false，或解码/传输异常 → interrupted，revision 0。
6. checked 完成成功且核对通过 → revision 恰好 1，内容为冻结 input.Content；store失败仅改变版本记录文案。nil store 不宣称已保存。
7. Job 已取消/调用已超时不被后到 completed 覆盖；沿用已有 Job guards，不侵入本片之外的生命周期设计。

### 9.4 Vue/API

1. 新 read validator 拒绝缺失/畸形 contentHash；正确 hash round-trip；deploy JSON 必含 baseContentHash。
2. load 磁盘 O/hash(O)，draft D/hash(D)：部署 D 使用 hash(O)。saveDraft 不改变磁盘 hash。
3. revision H/hash(H) 发布同样用 hash(O)，包括 loadRevision 后普通发布。
4. confirm 前冻结 A/hash(O)，confirm 期间继续编辑 B；提交仍 A/hash(O)，不得重复确认。
5. 任务成功、复读 A/hash(A)、当前 B：editor B、savedContent 原样、dirty true；再次提交 B 携 hash(A)。这是必须单独通过的核心验收。
6. 上例当前 editor 仍 A 也同样推进，但不擅自把它当作已保存草稿。
7. failed/cancelled/interrupted、preconflict/postconflict：不推进、不误报成功、文本不变。
8. completed 后复读 E/hash(E)：不推进到 E、不把 E 写入编辑器；下一次操作不能静默基于外部内容发布。显示已完成但待核对警告。
9. completed 后读取失败/缺目标/重复目标/坏 hash：不推进；历史刷新成功不能覆盖警告，历史刷新失败不能把任务历史结果改称失败。
10. 复读 pending 时继续编辑、切 activePath、切项目、关闭重开同项目、logout/unmount：只允许原 workspace/session 对冻结 target 更新；新工作区、其他文件、dirty文本均不被迟到响应覆盖。
11. 部署/复读期间重复点击仍只有一次请求；finally 不能清除新 workspace 的 deploying 状态。
12. 在无 `crypto.subtle`（必要时整个 global crypto 不可用）的环境下仍能完成 load → deploy → postread → baseline advancement。
13. 完整重跑已有 Compose 工作区事务测试，补全所有成功 postread fixtures，不能用 mock 默认 O 掩盖预期 A 的复读。

### 9.5 建议命令与验收声明

实施完毕后，在选定受控工作副本运行（路径/已有工具链以执行环境为准）：

- `go test -race -count=1 ./internal/compose`
- `go test -race -count=1 ./internal/agentsocket -run 'Compose'`
- `go test -race -count=1 ./internal/httpapi -run 'ComposeDeploy'`
- `go test ./api/openapi`
- `go vet ./internal/compose ./internal/agentsocket ./internal/httpapi`
- Go native 和 linux/arm64 编译；产物置仓库外。
- `pnpm test src/composables/useComposeWorkspace.test.ts src/api/docker.test.ts`
- `pnpm typecheck`、`pnpm lint`、`pnpm build`，再按 root 集成要求跑其他相关回归。

如果 Compose 聚焦测试仍有旧真实 Unix socket 用例，单独列明被环境限制的用例，追加明确的 bufconn 覆盖；不能跳过后声称全量通过。此任务禁止 Docker，故本片不执行 `docker compose config` 或部署健康检查，报告中明确未运行。Root 负责协调共享 OpenAPI、集成、提交、远端 CI 和发布；本交接不授权生产操作。

## 10. 不得夸大的保证

- 两次核对仍存在读后、rename 前的窗口；atomic rename 不等于 compare-and-swap。外部进程可在检查后继续修改。
- 成功后核对是一次时点事实。外部在核对之后、Job 完成/存 revision/浏览器复读之前再改文件，系统不能撤回已经真实发生的命令或保证最终磁盘不变；UI 复读只是更晚的观察，不是事务锁。
- 内容 hash 无法识别 A→B→A 的 ABA 历史，也不保护 inode、所有者、权限、父目录/symlink 别名、多 Compose 文件、`.env`、include、镜像 tag 或挂载数据。
- 同 Manager/规范化 target 锁不是同项目锁，也不协调另一个 Agent/进程、Lifecycle 或外部工具。
- 命令成功不证明容器健康；恢复配置/重新应用也不证明运行态完全恢复。
- 原 RPC 已取消/断开后，Agent 继续运行或恢复的结果仍可能不可送达；本片不新增恢复收据，也不把 interrupted 改称已恢复。
