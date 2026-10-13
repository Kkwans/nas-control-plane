# M08 会话撤销传播：下一实施切片交接

日期：2026-10-13。只读调查，不是实施完成报告。

仓库：`/workspace/scratch/a2c31a226a28/ncp`。开始检查时 HEAD 为 `c4f4473`；并行 Compose 工作随后提交到 `47c078c8e63245ef6add4da5577070545a14d65a`。本文引用的认证/终端文件未由本调查修改。已读根 `AGENTS.md`、roadmap、backend-audit；未启用或读取 OpenSpec 工作流。

## 1. 建议直接实施什么

实施一个后端纵切片 **M08-B：活动流的会话租约与有界关闭**：

1. 在现有 `auth.Service` 中增加活动流登记、到期计时和提交后的同步撤销标记。
2. 将四个现有入口 `/ws/terminal`、`/api/v1/system/events`、`/api/v1/logs/events`、`/api/v1/jobs/{jobID}/events` 的请求 context 绑定到真实数据库会话。
3. 注销、改密、禁用、删除账号成功提交时取消相应活动流；数据库会话到期时无需新请求即取消。
4. WebSocket 两向 I/O 使用被撤销的 context；取消时主动关闭浏览器连接和 Agent client transport，不依赖浏览器配合。
5. logs/jobs SSE 复用 system SSE 的逐帧 5 秒 Write+Flush deadline 约束，防止慢客户端阻塞关闭。

这可以独立部署，不需要 JWT、新 cookie、auth schema 迁移、通用消息总线、新 RPC、RBAC 或整个认证系统重写。

**此切片的完成口径**：撤销/到期传播到所有 Server 活动流；Server 停止接受该流的新输入和读输出；浏览器 WS 与 Server→Agent RPC 关闭；SSE 在既有写入预算内退出。**不能据此声称容器 exec、全部 PTY 子孙进程已经退出**。Agent 容器同步写入/清理缺口作为 M08-C 单独处理，见第 8 节。

保留登录 Root 用户现有主机/容器终端、Docker、文件、Compose、SQL 管理能力。只撤销会话的持续访问，不缩减 Root 功能，不碰部署权限、真实 NAS、生产数据或账号。

## 2. 当前事实与确切缺口

| 位置 | 现状 / 证据 | 实施意义 |
|---|---|---|
| `internal/auth/service.go:69–73,160–188,473–489` | 登录产生 32-byte 随机 token；Session 含 ExpiresAt；数据库只保存 token hash；默认 24 小时。 | 复用现有会话。不能误报明文 token 落库或无到期机制。 |
| `service.go:191–212` | Authenticate 查询 sessions JOIN users，要求 token hash、expires_at_unix > now、disabled=0；只返回 Principal。 | 新请求已做权限/到期判断；活动流拿不到到期值或稳定会话归属。 |
| `service.go:215–225` | Logout 删除指定 token hash 的记录。 | 单次 logout 应只撤销该 cookie/session，不撤销同账号其他独立登录。 |
| `service.go:292–331,334–404` | 禁用/改密在事务里删除目标用户所有 session；删除用户由 FK cascade 删除 session；没有活动流通知。 | 禁用、改密、删除必须撤销该用户全部活动流；重新启用不能复活原流。 |
| `service.go:492–533` | sessions 主键 token_hash；user_id FK ON DELETE CASCADE；秒精度到期值；没有 auth version、事件表、refresh token。 | 登记可使用内部 hash + userID；首片不需要加表。 |
| `internal/httpapi/auth.go:19–25,123–163` | Authenticator 只有普通 Authenticate；中间件只在请求入站保存 Principal。 | 不能从当前 Principal 推断具体 cookie/session 或到期；注册必须重新进行权威验证。 |
| `internal/httpapi/terminal.go:50–59,103–138` | WS accept 后只建立 request context + 默认 30 分钟终端 timeout；循环尚无会话级取消。 | cookie 过期/DB 删除不会自动关闭已经建立的 WS。 |
| `internal/httpapi/realtime.go:84–179` | system SSE 观察 request context；64 订阅上限；逐帧写/flush 5 秒 deadline；summary 是只读服务采集器快照。 | 绑定新的请求 context；保留已有 deadline、配额、共享采集 owner。 |
| `internal/httpapi/logs.go:70–147` | logs SSE 观察 request context，但同步 Write/Flush 无 deadline，初始写错误忽略。 | cancel 到达也不能打断已阻塞的写；必须同时补写入预算。 |
| `internal/httpapi/jobs.go:221–252` | job SSE 观察 request context，但忽略 Write 错误且 Flush 无 error/deadline；循环先读/发再 select。 | 既要补 deadline，也要在每次快照读取/发帧前检查取消。 |
| `internal/httpapi/middleware.go:104–117` | responseRecorder 已支持 Unwrap 和 FlushError。 | 可以直接复用 ResponseController；不能回退到吞错的 Flusher.Flush。 |
| `cmd/ncp-server/main.go:159–163,203–235` | 生产只有一个 auth.Service；HTTP BaseContext 来自服务 ownerContext。Linux DB owner lock 在初始化前获取。 | 首片可明确单进程服务契约；沿用 owner shutdown，不引入跨进程总线。 |

`8d75639` 修复了 HTTP/Agent 输入泵向已无人消费的满 channel 发送时不能取消的问题。现存 fake 测试已重跑通过。它没有实现 auth 撤销、HTTP 完整 proxy 清理验收或容器同步写入的有界取消，不应把 M08 标为整体完成。

## 3. 最小接口与归属

保留当前 `Authenticator`、`Session` cookie/JSON、普通 HTTP 验证接口。新增一个仅供长连接使用的 facet，例如：

```go
// 名称可按仓库风格调整；这是契约示意，不是已提交代码。
type StreamAuthenticator interface {
    AcquireStream(context.Context, string) (auth.StreamLease, error)
}
// StreamLease 只供进程内部使用，不是 API 响应。
// 暴露 Principal、Context、ExpiresAt、幂等 Release；绝不暴露 token/hash。
```

`auth.Service` 持有 registry。键为内部固定长 hash（可使用 `[32]byte`），条目记录 userID、唯一 lease ID、cancel cause、到期 timer 和一次性 release。一个 cookie 可拥有多个流；每个流独立 release；按 hash 和 userID 可撤销，不持久化 registry。

建议默认总租约上限 256，作为 WS+全部 SSE 的资源保护；保留现有 system 64 和 Agent 终端 session 上限。上限通过内部 Options 可注入测试，返回稳定 `AUTH_STREAM_LIMIT_REACHED`/503，不修改数据库会话，不淘汰其他正常用户流。未授权请求不能占名额。若实现者选择其他值，要写清依据与测试，不应无界登记。

HTTP 增加一个小的 `requireLiveSession` wrapper，仅包四个流入口，必须在 WS upgrade、SSE headers、任何 Agent RPC 之前成功获取 lease。即使普通 requireAuthentication 已运行，也不能用它稍早得到的 Principal 来代替租约验证。wrapper 用 lease 的最新 Principal 覆盖 context，defer Release，再调用 handler。

若 Auth 非 nil 但不实现 StreamAuthenticator：流入口以 `AUTH_STREAM_UNAVAILABLE`/503 拒绝，不能静默退回入站一次验证。可保留现有 Auth:nil 测试构造语义，但不得新增生产免认证开关；生产 main 对实际 auth.Service 接入做编译/构造验证。Auth:nil 生产 fail-closed 的全局改造仍属 M10，不能借首片扩大范围。

## 4. 登记与撤销必须有明确线性化顺序

### 推荐：小型 admission gate + 独立 registry mutex

- `lifecycleMu`（RWMutex）：协调 auth 会话验证/登记与会话删除事务。它是授权先后顺序 gate，不是活动流注册表锁。
- `streamsMu`（Mutex）：只操作 map、计数、timer/lease 状态；绝不持有它做 SQLite、bcrypt、网络写、等待 goroutine、PTY close。
- 唯一嵌套锁序：`lifecycleMu → streamsMu`。Release、timer、父 context 取消只拿 streamsMu，不能反向获取 lifecycleMu。
- lease cancel/timer stop 在从 map 摘出后执行，不能让锁内动作触发网络/其他阻塞回调。

AcquireStream 顺序：

1. 校验 token 非空、父 context 未结束，取 hash；获取 lifecycleMu.RLock。
2. 使用单独的短查询 context（建议 2 秒，仍继承 request 取消）查询 Principal、userID、实际 `expires_at_unix`，判断 enabled 和未到期。**这个 2 秒查询 context 不能成为整个流的父 context**。
3. 在 RLock 尚未释放时登记 lease；登记期间再次检查父 context 和到期。设置 timer、父取消回收钩子；成功返回前保证两者已受管理。
4. 释放 gate，返回 lease。普通 request cancellation、expiry、revoke 或 Release 任一个都使它不可恢复；cleanup 使用 once/idempotent 状态机。

Logout/禁用/改密/删除顺序：

1. 参数、密码策略、bcrypt 等耗时工作放在 gate 外；事务所需的身份/密码状态在 gate 内再次校验（见下一段）。
2. 获取 lifecycleMu.Lock；执行原有 SQL/事务，用独立有界 DB 操作预算。
3. **确认提交成功之后、释放 gate/返回 API 成功之前**，从 registry 摘出目标租约并同步调用 cancel(cause)。不能 fire-and-forget 广播；不要等实际网络/PTY 清理结束才返回。
4. 释放 gate，进行非必要返回数据读取。特别是 SetUserDisabled 现有 `userByID` 在 Commit 后可能失败：撤销必须紧跟成功 Commit，不能放在整个函数无 error 的 return 之后。
5. 明确未提交的失败/回滚不发“成功撤销”事件，也不能清 cookie 后返回成功。若数据库返回的是无法确认提交状态的错误，可保守关闭受影响的活跃流并返回 `AUTH_UNAVAILABLE`，但不能宣称持久会话已撤销；重新准入仍以 DB 查询为准。

该顺序消除两种经典漏取消：

- 验证通过 → logout 提交/广播 → 才登记：不可能，因为验证到登记连续持有 RLock；logout 必须等登记完成后取消。
- logout 已提交 → 旧 Principal 登记：不可能，因为新 Acquire 等 WLock 释放后重新查 DB，旧 session 已不存在。

不要用“先 Authenticate，再独立 Subscribe”或“订阅仅 future events”替代上述原子性。也不要只在 HTTP logout handler 发通知：改密、禁用、删除、其他直接调用 Service 的路径都会漏。

### 相邻的登录竞态须显式处理或降级说明

现有 Login (`service.go:171–188`) 先读密码并 bcrypt 比对，之后分两次 Exec 更新登录时间和插入 session。并发重置密码可在旧密码验证之后删除全部 session，而旧 Login 随后插入一张新 session。这是静态交错推导，未在本调查运行复现。

建议在首片一并做一个小修正：bcrypt 留在 gate 外；最终登录事务在 gate 内确认用户仍启用、password_hash 仍等于当初验证的 hash，再更新 last_login 和插入 token。重新检查失败返回原有 `AUTH_INVALID_CREDENTIALS`，不能静默接受旧密码。可用事务内条件 INSERT/明确复核，不引入凭据版本表。UpdatePassword 的自身当前密码验证同样在提交时核对其观察的 hash，避免两个迟到改密覆盖。用户删除/重建不能凭旧 ID/密码 hash 快照创建合法会话。禁用后重新启用只允许经当前状态与凭据复核的新登录，绝不复活原有 token/lease；若还要求取消跨越禁用→启用的在途 Login，需另外定义登录 epoch 或把该认证阶段与撤销序列化，不能假称单次最终状态复核覆盖这种 ABA。

如果实现者暂不做这点，M08-B 可以证明已有租约取消，但不得宣称“并发旧密码登录也被彻底阻断”；需要独立遗留项和测试证据。

## 5. 到期、关闭及错误生命周期

- 到期来自 DB 的 `time.Unix(expires_at_unix, 0)`。不要直接使用登录 JSON 中可能带亚秒的 ExpiresAt，避免比普通认证实际到期晚近 1 秒。
- 一条活跃 lease 一个可停止 timer，或等价的统一有界 timer owner。只管理活跃流，不为数据库中全部 session 启 goroutine/计时器。
- 到期不依赖新 login 清理过期记录，也不等待 SSE 下一次 tick（interval 可达 300 秒）。到期事件直接 cancel。
- 与现有 Options.Clock 一起注入可控 timer/调度器，使时间测试无需真实长睡眠；只注入 Clock、timer 仍使用真实时钟会导致不可靠测试。
- 原因建议内部稳定枚举：logged_out、password_changed、user_disabled、user_deleted、expired、service_closed；不返回用户 password/hash/token。
- release/expiry/parent cancellation/revocation 并发只能清理一次；map 和 timer 都归零。父 context 已取消、timer 恰在登记时触发也不能漏清理。
- Service.Close 先关闭 admission 并取消全部租约，再关闭 DB；不能让 Close 后的新 lease 登记成功。HTTP ownerContext 的取消仍有效，不用在退出时伪造 logout。
- 只承诺当前单 owner Service 的即时撤销。多进程读同一 SQLite、直接 SQL 篡改、其他程序复用另一 auth.Service 不会收到本地通知。当前 Linux serve 的 DB owner lock 有助保证生产单 owner，但不是对任意嵌入者的分布式一致性保证。
- 为每 lease 再轮询数据库不是首片必要条件。若未来支持多 owner，另加带上限的再认证或持久撤销版本，明确时延；不能以无界 polling 填补契约空洞。

## 6. Server WebSocket 最小实施边界

现有 TerminalClient.Open(ctx) 已能把 context 传到 gRPC，`agentsocket/terminal_client.go:18–34`；Close 会关闭 send 和专用 client connection（65–71）。不需要增加含用户密码或 token 的 Agent RPC。

1. lease 在 websocket.Accept 之前建立；终端自身 30 分钟 timeout 保留，父是 lease.Context。实际寿命是 request/owner、session 到期/撤销和 terminal timeout 的最早者。
2. 抽出小型双向代理 runner，依赖 reader/writer/close 及 TerminalStream 接口；真实 handler 只做 upgrade 和注入。这样可纯 fake 验证完整清理顺序，无需 httptest.NewServer 或真实 socket。
3. 取消发生时立即 CloseNow 浏览器连接，并关闭/取消 Agent client stream；关闭动作一次化，不能只等主 select 选择 Done。父 context 本身必须能打断 gRPC Send/Recv；fake 契约必须模拟这一点。注意当前 TerminalStream.Close 先 CloseSend，再 connection.Close；gRPC 1.82.1 的 `stream.go:107–109,132–135` 明确禁止 CloseSend 与 SendMsg 并发。不要从 cancel watcher 原样并发调用这个 Close。可依赖 Open 的 context 先解除 Send，再串行 Close；如要独立硬关闭，则给 client transport 一个只做 `ClientConn.Close` 的幂等安全 abort，或将现有 Close 改成直接关闭专用连接，不并发 CloseSend。不得为修清理顺序引入新 data race。
4. input/output 两端均有 done 通知。HTTP 清理顺序为取消 → 关闭 transport（唤醒阻塞 Read/Write/Recv/Send）→ 等待本端泵退出 → release。不能先等待输出泵、后关闭让 Recv 退出的 stream。
5. 两个处理 loop 在出队后、调用 Send/Write 前重新检查 ctx.Err；初始 MessageStart 之前也检查。Go select 对多个 ready case 随机选择，仅有 `case <-Done` 并不能阻止撤销后 buffered input 被选中。
6. 对一次已经进入 Send/内核缓冲的输入不能承诺撤回；准确界线是撤销标记之后不再准入新的处理，取消传输中止尚未完成的 I/O。通过有限 context 检查不能保证跨进程“提交那一纳秒零字节到达”。严格保证全部 Agent 写均已停止需要 Agent 清理确认与 M08-C，不在首片隐含扩大。
7. 不以成功发送最后一条 error/close 帧作为安全前提。复用已取消 context 写错误帧可能立即失败，这是正常；CloseNow 优先。可选择最佳努力 1008 close 通知，但绝不能引入等待 peer close handshake 的无界延迟。
8. 错误原因不得由普通网络失败猜成账号撤销。首片允许前端显示连接已断开，清楚记录认证失败原因映射留待下一小片；后续重连要重新通过租约验证。

Agent 侧现有 `receiveTerminalMessages` 注释（terminal_service.go:155–166）明确 Recv 由 RPC 返回后的 transport 取消唤醒。**不要在 Agent Open 返回前等待只能靠 Open 返回解除的 Recv**。首片只强化出队后 context 检查、保留该生命周期，不引入此死锁。

## 7. 三类 SSE 的正确接法

所有 SSE 使用 lease 绑定的 request.Context，不再额外创建 Background 流。正常断开只 release lease。

system SSE：
- 保留 `SummarySnapshots.Latest/Done` 只读接口。连接撤销不得 Stop/Close 共享 collector，不增加每连接采集、不因无连接停止采集，不改变历史持久化或 freshness。
- 保留 Docker request-scoped timeout；lease 撤销传到对应 Docker RPC。
- 保留 64 subscriber 与每帧 5 秒 Write+Flush deadline。退出仍 Add(-1)、stop ticker/heartbeat、清除 deadline。
- 每次 heartbeat/snapshot 写前检查 context；在一次已开始的同步 Write 中发生取消，最多等待当前 5 秒写预算，不必为“立即”并发操作 ResponseWriter。

logs/jobs SSE：
- 抽取现有 `writeRealtimeFrame` 的小通用 writer，仍使用 ResponseController.SetWriteDeadline + Write + Flush（有 error）+ 清 deadline；不复制三套不同的错误处理。
- 在 headers 提交之前验证 deadline 支持；不支持时按已有模块稳定 unsupported code 返回失败。禁止为了让 httptest.ResponseRecorder 通过而悄悄取消生产 deadline。
- 每次发送前和从 Agent/Job snapshot 返回之后检查 ctx.Err；cancel 与 tick 同时 ready 不允许继续查询/发新的数据帧。
- 初始 retry 帧、日志 unavailable/heartbeat、job progress 都必须走同样预算；Write/Flush 错误直接退出，不能忽略。
- job SSE 注销只结束观察，不调用 Job.Cancel/Delete，不更改 job 状态；已经 accepted 的 pull/Compose Job 继续按原 owner/明确 cancel API 执行。
- 首片不增加 SSE payload 或新的 `auth` event，既有 snapshot/logs/progress wire 兼容。停止网络流本身无需把撤销原因发到共享事件频道。
- stream 已经提交 200 后不得再写 JSON 401/503。新的 reconnect 请求才由 auth middleware 以原有 HTTP 401/503 拒绝。

这些流依然是只读观察。取消会话不应变成取消采集器、停止容器、撤销已落地 Compose 配置，或把任务标成 cancelled。

## 8. 下一独立切片：Agent 物理资源回收

以下是确定的静态阻塞链，不能被 B 的通过掩盖：

- `terminal_service.go:89–95` 在 select 内同步调用 Manager.Write/Resize。
- `terminal/manager.go:169–197` 的 Session.Write/Resize 没有 context；Close 在 map 删除后才调用 Session.Close，map 计数归零不等于 process 已停止。
- `terminal/container.go:146–151` 直接写 Writer、Resize 使用 Background。
- `container.go:154–175` Close 先写 Ctrl-C、exit，再启动 2 秒 WaitForExit context；前面的任意 Write 卡住时，2 秒预算根本尚未开始。
- `terminal/container_moby.go:77–89` CancelRead 也先同步写 Ctrl-C/exit，最后才 attachment.Close；因此取消读取的钩子本身会被写堵塞。
- `terminal/host_linux.go:282–294` 杀的是 command.Process 并关闭 master PTY；没有证明整棵进程树、已经 detach 的任务终止。不应说退出终端能撤销之前运行的所有 root 命令。

M08-C 建议：把 transport Abort（关闭连接、唤醒 R/W）与有界 best-effort graceful close 分开；容器写入支持 deadline/取消；Resize 继承 session context；取消回调不得先执行潜在阻塞写。Graceful 总预算从第一步起算，最后无条件关闭 attachment，WaitForExit timeout 为明确未确认状态。假设“io.Writer 任意实现都能中断”不成立，需定义并验证 attachment 的 interrupt 契约，不能另起永不 join 的 goroutine 来假装解决。

不应因验证进程退出超时而停止整个用户容器；这会超出终端资源回收语义且破坏 Root NAS 日常使用。

## 9. 前端兼容与下一最小改进

当前前端已有可复用基础：

- `frontend/src/session/sessionLifecycle.ts` 提供 generation + AbortSignal。
- `App.vue:30–41,106–114` 本标签显式退出先 invalidate/关闭状态订阅，再调用 logout；失败提示“服务端会话尚未确认”。
- `stores/auth.ts:42–67` 正确区分临时 status 查询失败与实际未认证，不能把网络断线改成已撤销。
- `api/docker.ts:96–145` job subscription 收到 session abort 会关流；断流不是任务 cancelled。
- `useLogQuerySession.ts:98–123` 和 `stores/system.ts:343–370` 有 session/stream generation 防迟到写回。
- `TerminalView.vue:168–235,333–363` 没有 captureSession，依赖组件卸载来 close；显式 logout 等 auth 请求完成前组件仍可能存在。旧 socket callbacks 引用当前全局 socket/terminal，不绑定连接实例。
- login 返回的 ExpiresAt 没存入 auth store；auth/status 本身不返回 expiry；EventSource error 也拿不到可靠 HTTP 状态。system/log 错误可停留重连状态，终端 close 文案是一般断线。
- `UsersView.vue:121–125` self 改密 600ms 后 reload；重置他人提示既有 session 已撤销，目前不能把该文案理解为活动 PTY 已结束。

B 后端可以先部署且不改协议。后续很小的 B-UI：终端每次 connect 捕获本地 socket identity 和 SessionContext，abort 立即禁用输入/清 paste/关闭本实例；旧 callbacks 不得修改新 terminal。每类异常 close/error 触发一次合并的 auth/status 再核对，只有明确 authenticated=false 才更新 auth store；网络/503 保留当前身份及失败提示。多个流同时出错去重，旧 generation 查询结果不得退出后来新登录。

不把 401 猜测、单纯网络 error 或浏览器 timer 当服务端安全边界；不自动重放终端输入、任务提交或 SQL。可以日后增加同源 BroadcastChannel 提升跨标签体验，但其消息不是授权证据，也不是首片安全依赖。

## 10. 必须覆盖的隔离测试矩阵

全部采用 fake、内存/临时 SQLite、可控 clock/timer。直接调用 handler/runner 和 custom ResponseWriter；不启动 HTTP/Unix socket、Docker、真实 PTY、NAS 服务。

### Auth lease/事务交错

1. 同一 cookie 的 WS、system、logs、job lease 都因 Logout 同时取消；同账号另一个 token 和另一个账号保持活跃。
2. 改密、禁用、删除目标账号所有 token lease 取消；actor 不受影响（self 改密除外）；重新启用不复活旧 lease。
3. 操作参数失败、当前密码错误、最后账号保护、DELETE/UPDATE 注入失败回滚，不发成功撤销；成功 Commit 后 userByID 读取失败仍已取消。
4. Acquire 在 SQL 验证后/登记前暂停：Logout 等待；放行 Acquire 后 Logout 返回时新 lease 已取消。
5. Logout 在 Commit 前/后暂停：新 Acquire 不能绕过 gate；提交完成后旧 token 永不成功注册。
6. 父 context 已取消、到期与登记同 tick、lease limit、重复 Release、release/expiry/revoke/Close 并发：计数和 timers 最终归零，无 double close/panic/data race。
7. fake clock 精确跨 DB Unix 秒边界；300 秒 SSE interval 不影响到期取消；终端 timeout 更早时正确清理 session lease。
8. Login 已验证旧密码后暂停，再改密/禁用/删除：恢复 Login 不得生成可用新会话；此项是上一节竞态修正的验收，不应只测已有 token。
9. Service.Close 与 Acquire 交错，关闭后无新 lease；不同 handler 共用同 Service 的流全部按正确归属取消。

### HTTP admission 与双向流

10. 在四个入口普通认证通过后、lease acquire 前撤销：返回 401，不 upgrade、不写 SSE 200、不调用 Agent、不增加 subscriber。
11. provider 不支持 live lease 时非 nil Auth 返回 503，无不安全兼容 fallback；wrapper 更新正确 Principal/actor，无 token/hash 进入日志。
12. fake native WS reader/writer、fake TerminalStream 验证全部阶段：Open 阻塞、初始 Send/Recv 阻塞、buffered input、input full、output Recv 阻塞、WS Write 阻塞、浏览器正常断开、terminal timeout、lease revoke。
13. 已取消 context 与输入 ready 同时出现时，不再调用新的 Agent Send；同样验证 Agent 输入 out-of-queue 检查。in-flight Send 可以失败返回，不能被测试误定义成可撤销的已生效命令。
14. 每条退出路径精确验证 cancel/CloseNow/Stream.Close/done/release 顺序；两个泵均确认退出；不以 goroutine 总数量瞬时波动为唯一泄漏证明。
15. Agent fake Recv 只在 transport cancellation 才退出，证明 Open 不先等待它；这一点防回归死锁。

### SSE 与业务隔离

16. 三类 SSE 在 idle、tick、读 Agent/Job 时取消均退出；cancel 后没有新的业务帧。
17. 初始写、body Write、Flush、clear deadline、unsupported deadline 失败均正确退出；慢 writer 仅阻塞自己的流且 <=5 秒预算，slot/ticker/lease 回收。
18. system 撤销某用户订阅后其他用户订阅正常；共享 collector 不被停止，source 计数不因租约数变化；Docker 调用观察取消。
19. job 流撤销后 fake Job 继续 running→completed；没有 cancel 回调、状态修改或重复执行；重新登录可重新观察原 Job。
20. logs request clone 保留 lease context；cancel 时不继续采集/发送 unavailable heartbeat；不运行真实 journald。
21. race suite 多次重复精确 barrier 交错；测试 timer/contexts 不在 race 中使用未同步可变 now。

### 后续前端 fixture

22. 本标签 logout 请求挂起时终端已不能发送；旧 WS late close/message 不影响新连接；会话更替清敏感 pending paste。
23. 异常断流触发去重 status 检查；明确未认证退出/清理；网络/503 仍保留身份；旧 status 不污染新登录。
24. Job observation closed 只终止 UI 观察，不显示“任务已取消”；支持重新观察。

## 11. 文件边界与交付顺序

建议 B 的文件范围：

- 新 `internal/auth/streams.go` / tests；`service.go` 的少量 lifecycle 接点与登录最终复核。
- 新 `internal/httpapi/session_streams.go` / tests；`handler.go` 四个路由绑定，不改普通路由策略。
- `terminal.go` 小 runner/接口提取与清理；对应纯 fake 测试；`agentsocket/terminal_client.go` 的并发安全硬关闭，以及必要的 `terminal_service.go` 出队后 ctx 检查；不改 RPC 结构。
- 新或提取 SSE writer；`realtime.go` 复用、`logs.go`、`jobs.go` 小接入；deadline fake tests。
- `cmd/ncp-server/main.go` 仅保证 stream capability 接入/生命周期；不改部署配置。
- OpenAPI 只同步新增稳定 admission 错误/流关闭语义及 endpoint 描述，不改 payload。roadmap/QA 如实记录 B 与 C 尚余边界。

避免触碰本轮并行 Compose 工作区、SQL 截图脚本、监控采集设计、Job 引擎。`jobs.go` 只修改 SSE 函数；`handler.go`/OpenAPI 属共享文件，动手前与主任务检查最新 diff。

建议先 auth lease 和 deterministic race tests，再 four-route admission/WS runner，最后复用 SSE writer。它们可多个中文 commit，但只有四路、WS 清理与 SSE 写预算全部验证后才称 B 可部署。不能只交付 registry 后宣称会话撤销贯通。

源代码 commit/push 与 CI 成功均不等于已经在 NAS 部署。本文不授权 NAS/服务启动/权限或凭据变更。

## 12. 本次实际验证与未验证

已运行 Go 1.26.4 的以下聚焦基线，全部通过：

```text
GOTOOLCHAIN=local GOPROXY=off \
GOMODCACHE=/workspace/scratch/a2c31a226a28/ncp-tools/mod \
GOCACHE=/workspace/scratch/a2c31a226a28/ncp-tools/go-cache \
/workspace/scratch/a2c31a226a28/ncp-tools/go/bin/go test \
  ./internal/auth ./internal/httpapi ./internal/agentsocket \
  -run '^(TestBootstrapCreatesOneRootAccountAndLoginSession|TestLoginRejectsWrongCredentialsAndExpiredSessions|TestUserManagementLifecycleAndSessionRevocation|TestUserManagementProtectsCurrentAndLastEnabledUser|TestPasswordPolicyDefaultsToSixCharactersAndPersistsRules|TestTerminalInput.*|TestTerminalReceive.*)$' -count=1
```

结果：auth 1.337s、httpapi 0.028s、agentsocket 0.027s。同一精确选择增加 `-race` 后也通过：auth 15.548s、httpapi 1.044s、agentsocket 1.048s。这些测试证明当前 SQLite 会话规则及 8d75639 输入泵基线；**没有测试尚未实现的活动流撤销**。

本调查不运行包级全测试，因 existing `terminal_test.go` / `terminal_linux_integration_test.go` / `terminal/host_linux_test.go` 包含真实 listener、Unix socket 或 PTY。未来实现若沿用上面的允许范围，必须使用精确测试名选择；不能误把 `go test ./...` 当作纯 fake。

未运行：新方案测试、完整 Go build/vet、前端 lint/typecheck/build、浏览器、真实 Docker/PTY/NAS；未改 repo、commit、push、部署、账号或访问设置。
