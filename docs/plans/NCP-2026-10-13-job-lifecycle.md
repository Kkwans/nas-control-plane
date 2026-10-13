# M06：Job 持久化与取消生命周期详细方案（仅设计）

日期：2026-10-13。基线：本地 ncp HEAD bfbe091（包含 M01 snapshot 修复）；检查时 Compose 两个文件存在他人工作区修改，本方案未修改仓库业务文件。依据 AGENTS.md、后端审计 B05/B06、实际 Go/TypeScript/OpenAPI 和现有测试。未运行 Docker、NAS、部署或运行测试；下文测试是待实施验收场景，不是已通过结果。

## 1. 结论与边界

先保留现有 HTTP 路由、六种公开 status、JobSnapshot 及 M01 深拷贝边界。用“每 Job 串行生命周期写入 + 仅创建允许 INSERT + 版本条件 UPDATE/DELETE + 有界执行队列”修复可靠性，不做持久任务队列、不自动重放 NAS 操作、不一次迁移全部同步接口。

取消是请求，不是停止证明。内部可有 cancelling 阶段，v1 对外继续返回 queued/running，新增可选 cancellationStatus=requested；只有执行器真正确认停止才能写 cancelled。现有 Agent RPC 缺少远端取消确认协议，运行中取消后仅得到 gRPC Canceled 时，应写 interrupted 并明确结果未知，而不是“下载已停止”。排队且尚未发出 RPC 的任务可以可靠 cancelled。

此设计保证已返回 202 的创建有持久记录；不承诺操作恰好一次、不承诺服务器崩溃后自动续跑、不承诺取消撤销已发生的 Docker 副作用、不承诺每条进度都落盘。

## 2. 代码事实与限制

- internal/httpapi/jobs.go：M01 已对 Layers、CompletedAt 在锁内复制，create/get/list/cancel/delete 和持久化快照脱离内部对象。保留 cloneJobSnapshot；这是并发安全修复，不是持久化顺序修复。
- update 仍 go persist；progress/expectedTotal/completePull 同步 persist；persist 忽略 JSON/DB 错误。混合写入可以将 completed 覆盖为 running。create 在内存发布后落库，且无法向调用者返回失败。
- delete 先内存删除、DB 失败再恢复；旧 UpsertJob 会重新 INSERT；当前禁止删除的判断只列 queued/running。
- controlstore/store.go UpsertJob 无 revision；MarkRunningJobsInterrupted 只更新 queued/running；newJobRegistry 丢弃恢复/加载失败且仅装载最近 500 条。JobRecord 没有 ArtifactState；该字段以实时 inventory 复核为准，不应新增 Docker 全量数据库镜像。
- docker_images.go 在 goroutine 中才 setCancel；pullSlots 限制并发 3，却不限制排队 goroutine；执行 timeout 从排队前开始；slot 与 ctx 同时可用可能继续启动。成功路径 completePull 和 update 两次独立写入。
- Compose deploy 另起无界 goroutine，不注册取消；RecordComposeRevision 失败目前仍区分“部署成功但版本保存失败”。不要把此类附加记录失败改写成部署失败。
- agentsocket/client.go PullDockerImageWithProgress：ctx 取消能使本地 Recv 返回，但没有 Agent 操作 ID/远端停止 ACK。完成帧返回 Completed 值，HTTP 当前忽略结果内容；实施时应验证 Completed=true 及必要结果字段。
- frontend/src/api/docker.ts 的 parseJobSnapshot 严格枚举六种 status；新增 cancelling 会被旧页面直接拒绝。useDockerImageDownloads 仅 queued/running 建立订阅，Panel 以这两种禁止删除；stopDownload 目前忽略 cancel 返回快照。OpenAPI cancel 202 文案是“任务已停止”，必须修正。
- cmd/ncp-server/main.go 只做 HTTP Shutdown，没有 Job engine stop/drain。NewHandler 返回 http.Handler，不能直接改成双返回值而忽略其他调用点。

## 3. 最小结构与并发约束

保留 httpapi 中的外部 snapshot，抽出小型 Job engine（可以先同包独立文件，不以新包作为交付目标）。

每个 entry 包含：生命周期串行锁 opMu、快照/进度短锁 viewMu、持久 revision、已提交状态、最新进度、可选 pendingTransition、cancelRequested、executor completion、不可复用的实例 token。registry.mu 仅用于 ID→entry 的索引读取/插入/移除；查到指针即释放，再锁 entry。禁止持 registry.mu 做 DB I/O、等待队列、调用 cancel、Agent RPC 或等待 worker。

所有生命周期修改必须经过 entry.opMu：创建发布、开始执行、取消请求、结束、删除、恢复结果应用。每个 Job 可以在 opMu 下做有界 DB I/O；不同 Job 的读取/取消不被 registry 大锁阻塞。get/list 只短暂取 viewMu 并深拷贝。禁止回调持 viewMu 再等待 opMu，避免锁序反转。

进度回调只更新 viewMu 下的最新值和 dirty generation，不为每条消息新建 goroutine，不做 DB I/O。一个固定 flusher 定时扫 active entries，逐个取 opMu，重新采集最新 snapshot，执行有 timeout 的 UPDATE。一次 flush 只确认捕获的 dirty generation；其间新进度不能被误清除。生命周期写携带最新进度，可顺带清 dirty；终态提交后忽略所有晚到进度。snapshot 更新序号独立于 DB revision，SSE 用更新序号检测变化，避免秒级存储时间和相同时间戳丢通知。

执行器使用固定 worker pool 与有界队列，拉取保留并发 3；Compose 使用独立小池和队列（例如并发 1，避免被 pull 饥饿，但不把它误当作跨请求项目事务锁）。容量值成为明确配置/常量，压测后定值。队列满返回 503 JOB_QUEUE_FULL；不创建一个 goroutine 去等空位。完成待落盘、排队、创建预留都计入有限容量，DB 故障不会堆积无限 pending results。

取消 ctx 在 admission 时构造并安装，早于发布 ID。排队 deadline 与执行 deadline 分离；运行计时从领取执行资格开始。worker 领取后在同一 opMu 协议检查取消标记，再提交 running，再发 RPC。取消先线性化则不得发 RPC；开始先线性化则按照运行中取消处理，不用 select 的随机选择作业务裁决。

## 4. 持久化顺序与错误语义

### 创建（包括 retry、Compose）

1. 完成输入校验；预留有限队列容量；生成新 ID 和 entry/context。retry 始终新 ID，旧任务不变；ExpectedBytes 纳入初始 snapshot，不再 create 后补写。
2. 有界 context 下 CreateJob INSERT。失败释放预留和 context，不发布 ID、不调用 Agent、不返回 202。返回 503 JOB_PERSIST_FAILED，日志记录 jobId/阶段/稳定错误类别，禁止原样输出可能包含凭据的异常。
3. 成功后发布 entry 并把预留转换为排队项，然后返回 202。预留/发布/关闭由 admission 协议协调，持久化后不能因普通“队列已满”丢失排队项。
4. 持久化完成而进程崩溃于入队前：重启将 queued 标 interrupted，不自动重放。HTTP 响应丢失可能造成用户重试重复任务，本期不能宣传 exactly-once；幂等键属后续独立需求。

### 开始与终态

- queued→running 必须先成功 UPDATE，再发布 running 和调用 Agent。写入失败不调用 Agent，保留 queued，明确持久化异常；按 engine 管理的有界退避重试，不为重试起 goroutine。
- worker 结果先收敛为一个不可变 pending outcome，再一次持久化终态+进度+CompletedAt；消除 completePull/update 双写。
- 终态写失败：不把未保存 completed 发布成可靠完成，也不把真实执行成功改写为执行失败。公开 status 保持最后已提交的活动态，附加 persistenceStatus=pending 和 message“执行已返回，结果保存失败，正在重试”。entry 内部禁止再次调用 Agent；固定 flusher 重试原 outcome，保留有限容量。SSE 继续，不发送假终态。
- 重试持久化用独立于已取消 execution ctx 的有界 store context。存储持续失败时停止新 admission，并暴露 Job storage degraded；恢复后重试记录，不重做 NAS 操作。退出前仍未写入的 pending outcome 不能假称保存成功；下次恢复为 interrupted/结果未知。
- 进度写失败可合并重试，不把整个操作标失败；报告指标和脱敏日志，避免每帧刷屏。
- DB 操作必须有 timeout；仅使用 context.Background 而没有上限不可接受。事务提交结果如不确定，先按 ID/revision 读回确认，不盲目重新创建或重复执行。

### 删除与防复活

只允许已持久提交的终态且无在途本地执行 owner 的 entry 删除。内部 cancelling、finalizing/pending persistence 都算活动态。持 opMu 校验→条件 DeleteJob→成功后从索引移除并标 deleted；失败保留原 entry，不做“先删后补回”。晚到 callback 即使持有旧 entry 指针也必须检查 deleted/token/terminal，不修改也不落库。

核心不是只加一个内存 tombstone，而是 UPDATE 永远不得创建缺失行。先前持久化必须经过同一 opMu，故删除不能越过在途 flush；就算故障注入让旧 UPDATE 在删除后抵达，RowsAffected=0，不会 INSERT。M06 不需要无限期数据库墓碑。若未来重新引入异步跨进程 UPSERT、可重用 ID 或事件重放，则须新增永久版本/墓碑策略，不能沿用本期证明。

### 启动与退出

生产入口显式构造/初始化 engine，恢复成功后再开始服务/admission。恢复事务将旧 queued/running（包含取消请求）标为 interrupted、清速度、保留进度、增加 revision，message 说明“控制面重启，执行结果未确认”；随后加载失败必须显式报错，不假装空历史。

最小兼容方案：Config 可注入已初始化 engine，生产 main 构造失败直接退出；保留现有 NewHandler 签名供调用者，但测试 nil-store 行为须显式 memory 模式，不能 production 配置意外 type assertion 失败后默默降级。若保留构造器内初始化，至少暴露 readiness 错误并所有 Job 写接口 fail closed；推荐生产构造前置。

恢复只支持同一数据库一个有效 engine owner。CAS 能阻止旧写覆盖恢复状态，却不能阻止两个 server 同时执行资源操作。生产启动需持有明确的本地单实例租约/进程锁或经确认的一实例部署约束；不得声称多实例安全。活跃实例存在时不运行全量“重启中断”更新。

退出顺序：停止 admission→停 HTTP 新请求/允许既有结果返回→取消执行 ctx→有限等待 worker 回收→flush 已知结果→停止 flusher→关闭 store。共享总 shutdown budget，不给每阶段重新无限续时。排队未开始可 interrupted；运行中仅凭 timeout 不写 cancelled。worker 未退出则保留未知事实并上报，不能把锁/队列回收伪装成远端进程已停止。

## 5. 状态迁移表

内部 phase 不直接扩充 v1 status。cancellationStatus 可选枚举 requested/confirmed/unknown；无取消请求时省略。persistenceStatus 可选枚举 pending，正常省略。

| 来源 | 事件/证据 | 内部目标 / 对外 status | 完成时间与返回 |
|---|---|---|---|
| 未创建 | INSERT 成功且入队 | queued / queued | 无 CompletedAt；create 202 |
| queued | worker 获得资格且 running 提交成功 | executing / running | 无 CompletedAt |
| queued | 取消先于执行资格取得 | cancelled / cancelled | 持久化后填 CompletedAt；cancel 202 |
| running | 取消请求提交成功 | cancelling / running，requested | 无 CompletedAt；cancel 202 仅表示请求已接收 |
| cancelling | 重复取消 | 原状态 | 幂等 202，不重复触发 RPC/cancel |
| running 或 cancelling | 收到可信 Completed=true 结果 | completed / completed | 一次终态写成功后填 CompletedAt；取消未能阻止完成不伪装 cancelled |
| cancelling | 执行器明确停止确认且已本地收尾 | cancelled / cancelled，confirmed | 持久化后终态；不保证清除已下载层 |
| cancelling | 本地RPC取消/连接断开，无远端结果证明 | interrupted / interrupted，unknown | 本地 owner 已结束后持久化；message 说明远端状态未知 |
| running | 确认的执行失败 | failed / failed | 稳定 errorCode；不原样公开敏感 err |
| queued | 排队超时且未发RPC | failed / failed | JOB_QUEUE_TIMEOUT，不标用户取消 |
| running | 执行超时且远端结果未知 | interrupted / interrupted | JOB_EXECUTION_TIMEOUT；不宣称未发生副作用 |
| 任意待终态 | DB 写失败 | finalizing / 保留最后 durable status，pending | 无公开终态完成承诺，不再执行 |
| 终态 | 晚到进度/结束回调 | 原终态 | 忽略，记录低噪诊断；终态不相互覆盖 |
| cancelled | 重复取消 | cancelled | 幂等 202，返回原快照 |
| completed/failed/interrupted | 取消 | 原终态 | 409 JOB_CANCEL_UNAVAILABLE |
| 可删终态 | Delete 成功 | deleted / GET 404 | DELETE 204 |
| 旧活动态 | 单实例启动恢复 | interrupted / interrupted | 不自动重放，无停止保证 |

成功与取消的裁决：以 entry 串行化确认的执行结果为准。取消请求不是终态优先权；若请求先提交但 worker 随后拿到真实完成结果，允许 cancelling→completed。若 terminal 已提交，任何后续事件不可更改。取消 context 必须在锁外调用，不能让回调重入死锁；在同一请求内部先提交取消标记再调用 cancel，HTTP 202 在这两步后返回。

取消落库失败则不返回202、不声称已接收持久请求，也不触发新用户取消信号；返回503 JOB_PERSIST_FAILED。运行自然结束仍可继续走正常结果归档。进程 shutdown 的 cancel 不受此用户取消承诺约束，也不能误记为用户取消。

Compose 最小版声明 cancellable=false，保持409。先解决 B09 事务/回滚和明确 Agent 取消契约，再开放取消；不能机械复用 image pull 的 cancel。可选 cancellable 字段帮助UI，但服务端校验始终权威。

## 6. Store 接口与迁移必要性

建议接口（名称可随仓库风格调整）：

- CreateJob(ctx, record) error：纯 INSERT，ID 冲突返回稳定 ErrJobExists；仅 admission 使用。
- UpdateJob(ctx, record, expectedRevision) (newRevision, error)：UPDATE WHERE id=? AND revision=?；成功 revision+1。无行更新返回 ErrJobConflict/ErrJobNotFound，可读回区分，不吞掉 RowsAffected。
- DeleteJob(ctx, id, expectedRevision) error：条件 DELETE；缺失/冲突明确区分。
- Job(ctx,id) (record,error)：缓存未命中、CAS/提交不确定核对所需。
- Jobs(ctx,kind,limit) 保留用于兼容最近列表；之后可加独立分页接口。
- RecoverInterruptedJobs(ctx) error：显式错误，处理 cancellation 元信息和 revision；单实例初始化期间事务执行。

JobRecord 增加 revision（内部 int64）、cancel_requested_at 或 cancellation_status、可选稳定 error_code。迁移可重复执行，旧行 revision=1，取消字段默认空。persistenceStatus 是易失待写信息，不能只靠正在失败的 DB 记录它。cancellable 按 type/执行器能力派生；ArtifactState 继续实时验证，不新增持久 Docker truth。

为何改接口：只保留 Upsert 无法证明删除防复活；纯 Create/Update 分离是必要项。严格单 owner + 每 entry opMu 下 revision 并非排序所必需，但成本低，能显式发现 stale reference、恢复/未来维护路径的旧写，推荐纳入。revision 本身不能替代 opMu：CAS失败后不得把旧snapshot无脑重试到新revision；应读取当前状态并重新验证迁移。物理删除+UPDATE-only足够，本期不需要 tombstone 表或事件溯源。

旧 UpsertJob 如因迁移测试暂保留，必须不再被运行时 engine 使用，并加测试禁止删除后调用此路径；最好同一迁移切片移除旧方法及调用。fake store 必须实现新语义，不能只把所有 UPDATE 当作成功。

## 7. HTTP、UI、OpenAPI 兼容策略

保持路径及成功码：pull/retry/deploy 202 + JobSnapshot，cancel 202 + snapshot，delete 204，读取仍原结构。新错误使用稳定码（503 JOB_QUEUE_FULL/JOB_PERSIST_FAILED/JOB_ENGINE_UNAVAILABLE；现有404/409语义保留）；不得再依赖 err.Error()==某文本。

保持 status 六值是关键。新增可选 cancellationStatus/cancellable/persistenceStatus/errorCode，旧 parser 会保留未知字段且 status 仍可识别；旧UI最多继续显示“下载中”，message 明确“正在请求停止”，不会看到假“已停止”。新UI消费字段显示“取消中”、禁用重复按钮，并在 stopDownload 立即 upsert 返回快照；保留SSE直到真正终态。同步修订完成/未知结果的提示与重试风险。

OpenAPI 更新 cancel 202 为“取消请求已接收，可能仍在执行”；补充可选字段、现有 CompletedAt、404/409/503响应和状态语义。未来想把 cancelling 作为公开status，须做客户端先行升级/能力协商或版本化，不能以“新增枚举是兼容改动”跳过。

列表：本期明确既有接口是最近任务，保留 {jobs}。缓存未命中 GET 应查DB，避免第501条“存在却404”；内存保留全部活动entry + 有界终态LRU。可新增 opt-in cursor/limit 与 nextCursor，游标以(created_at,id)稳定排序，不能默认截断正在运行的任务；旧调用保留最近列表语义。持久记录保留期限/批量清理另立产品决定，不默认删除用户历史。不要把前端已有本地分页称作服务端分页。

未知结果 interrupted 的 retry 保持现有入口，但新UI要提醒检查资源状态后重试；本期不自动重试任何未知副作用操作。删除Job历史与删除镜像保持完全独立。

## 8. 可独立审阅提交切片

1. feat(jobs)：引入有版本的创建更新存储接口。仅controlstore/迁移/测试；旧接口迁移桥只在确有调用需要时短暂保留。验收临时SQLite、重复迁移、CAS失败、缺行UPDATE不创建、条件删除。未接入前不宣称B05修复。
2. fix(jobs)：顺序持久化与可靠接收。entry/opMu、构造错误、create先落库、状态写错误上抛、终态单写、删除协议；同时迁移pull/retry/Compose全部已有Job入口，保留M01。验收fake store故障及HTTP202门槛。不能只修image而留Compose绕写。
3. feat(jobs)：有界调度与资源收尾。替换go runImagePull及Compose裸goroutine、固定flusher、容量预留、生命周期Close、main初始化/shutdown；同步补queue-full OpenAPI。此项与2可按代码依赖顺序提交，发布可靠性声明须等二者完成。
4. fix(jobs)：取消状态机与兼容呈现。添加可选协议字段、提前context安装、排队/运行取消区分、终态单调、前端/SSE/OpenAPI同切片；Compose保持不可取消。没有Agent ACK时运行中取消诚实 interrupted。
5. test(jobs)：跨层故障回归与缓存恢复边界。fake barrier测试、临时SQLite restart、多于500条缓存未命中、前端解析/按钮/SSE测试；此切片是补充，1–4各自仍须带针对性测试。列表完整分页/保留策略和Agent停止ACK可再单独立项，非修B05/B06的“大重写前置”。

每片可独立提交和审阅不等于随意换序。1是2的依赖；2、3、4构成完整M06可交付边界。不要在1落地时提前宣称所有创建已可靠，或在只有取消ctx时宣布远端取消已保证。

## 9. 真实可执行验收场景（无需 Docker/NAS）

使用 channel barrier 控制事件顺序，不用 sleep 猜竞态。Fake Agent明确区分 started、ctxCanceled、localReturned、remoteStopped；只有最后一个是强停止证据。

1. 创建DB失败：pull/retry/Compose均503、无registry记录、无Agent调用；队列容量释放。阻塞INSERT时GET已知候选ID不能提前看到任务；返回202后用第二store连接读到queued或后继状态。
2. 同Job反转写：阻塞running写，触发完成/flush；证明后续生命周期不能越过，最终DB终态；不同Job的get/list及可用操作仍能完成，证明无registry大锁内I/O。
3. 终态写失败：Agent成功仅一次，公开不提前completed，重试DB成功后只出现一次终态；持久化重试不重复Agent调用；pending结果满时admission有界拒绝。
4. 删除防复活：持有旧entry/旧revision，终态删除成功后释放晚到progress/update；DB和GET持续不存在。删除失败保留原记录。缺行UpdateJob单独验证无法INSERT。并发重复删除返回约定204/404，不恢复旧对象。
5. 立即取消：admission后、worker领取前取消；Agent计数=0，cancelled持久化；slot同时可用仍不发RPC。排队取消写失败不假202。
6. 运行取消：fake收到ctx取消但阻塞返回，snapshot仍running+requested、CompletedAt空、不能delete。收到明确remoteStopped才cancelled；仅localReturned则interrupted+unknown；完全不响应的fake受shutdown测试budget约束，不用起无限替代worker。
7. 完成/取消两种顺序：completed先提交后取消409；requested先提交但收到可信成功结果则completed；终态后progress/重复completion不修改结果。Completed=false的完成帧不能成功。
8. 取消幂等与能力：requested重复202；cancelled重复202；其他终态409；Compose409且无cancel信号。队列/执行deadline和用户取消分别测试稳定errorCode，不能都cancelled。
9. 高频progress+多读者JSON+flush+cancel：race测试；读者篡改Layers/CompletedAt不能影响entry/DB/其他snapshot。flusher合并可丢中间进度，但terminal必须包含最终已知值，无终态倒退。
10. 重启临时SQLite：queued/running/取消请求恢复interrupted，completed不变，速度清零、字节数保留、revision递增、执行计数=0。恢复UPDATE失败和Jobs读取失败均使生产初始化失败，不回空历史成功。
11. 队列容量与shutdown：超过限额立即503；worker数固定；HTTP断线不取消已接收任务；停机后拒绝create，未结束者不假cancelled，store不得早于flush关闭。测试确认关键goroutine通过done channel收尾，不仅采样全局goroutine数。
12. 前端：旧六值+附加requested仍解析；取消中持续订阅、禁重按/删除、message准确；unknown interrupted显示结果未确认；SSE真正终态才关闭；503保留可理解错误，不显示成功toast。OpenAPI覆盖新增字段/错误码、取消202说明。
13. 历史边界：501+条记录，旧记录按ID可查；active记录不因LRU丢失；相同秒createdAt顺序稳定。job记录删除不调用镜像删除Agent。

现有测试改造注意：jobs_snapshot_test.go 的 expectedRecords=2+iterations*4、终态后仍进度写、cancel callback之后期待旧快照最后落盘，都绑定旧实现，必须改成“快照隔离 + 最终一致/终态单调”断言；不能把写次数降低误判为漏写。保留M01保护目的，不能直接删掉race覆盖。取消快照CompletedAt现在可为空，原测试无条件解引用需要按状态改。controlstore TestJobsPersistProgressAndInterruptRunningTasks 的 LayersJSON 是数组fixture，实际snapshot用map；加合法map fixture和畸形历史数据测试，明确损坏记录诊断，不静默伪造正确进度。

建议实施后的验证命令：go test -race ./internal/httpapi ./internal/controlstore；go test ./api/openapi；go build ./cmd/ncp-server ./cmd/ncp-agent；frontend pnpm test（定向Job/下载/Compose用例）、pnpm typecheck、pnpm build；若部署配置有变化再做纯Compose配置解析。上述本次均未执行；真实NAS健康、Docker真实取消与残留产物核验需要另获授权，不能用fake结果替代。
