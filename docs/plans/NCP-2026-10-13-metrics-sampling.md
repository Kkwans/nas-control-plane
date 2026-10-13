# M13：服务侧独立指标采集与共享实时快照

日期：2026-10-13。A已提交e776c17并通过隔离测试、独立审查及CI 38377913634；B/C仍为后续方案。未运行真实Agent、未连接NAS、未采集当前机器指标、未访问Docker。

调查基线：HEAD `7060732a416279ebf6886670e9f044df9bd40ca1`，工作树已有其他模块并行修改。本文所列行号以调查时文件为准，实施时按符号定位。已读 `AGENTS.md`、2026-10-13 roadmap/backend-audit/frontend-audit。遵守 Agent 独占宿主机能力，Server 管生命周期与 SQLite，浏览器只消费 API；不启用 OpenSpec。

## 1. 决策摘要

采用一个由 `ncp-server serve` 显式创建、启动、关闭的轻量 `internal/monitoring.Collector`。它通过现有 `agentsocket.CollectSystemSummary` RPC 每 5 秒取真实快照，保存一个最新快照；每分钟只持久化一个完整样本。HTTP summary 和 summary SSE 只读这个快照，浏览器断开不影响采集。沿用现有一分钟桶、7 天历史和 API 路径，不引入 Prometheus、消息队列、插件框架、事件溯源或第二个数据库。

M13 分开交付：A 独立历史采集及原子存储；B summary/SSE 共享与明确过期状态；C Docker inventory 共享及断线对账。A 不能宣称多标签采集已消重；A+B 才完成主机指标闭环。Docker 事件是 C 的独立后续，不阻塞 A/B，也不能把已有 PoC Events 当成生产订阅。

## 2. 已确认代码事实

- `internal/httpapi/handler.go`：`systemSummary` 调 `collectSystemSummary` 后调 `recordMetricSample`；`systemEvents` 每条连接有独立 ticker，按 scope 直接调 summary/Docker RPC，并写历史。默认 5 秒、客户端可选 2–300 秒，HTTP/SSE 多消费者会重复采集。
- `recordMetricSample` 调 `summary.MetricSample()`，转换为 `controlstore.MetricSample`，但丢弃 `RecordMetricSample` 错误。其 context 来自请求，因此连接生命周期影响落库。
- `internal/system/summary.go`：`SummaryCollector.Collect` 顺序采各源，CPU 采样等待 200ms；各源失败主要通过 `Summary.Warnings` 返回，可能没有整体 error。不能把 nil error 等同于所有字段有效。
- `internal/system/monitoring.go`：网络/磁盘是累计字节，温度是可空子样本；复用该转换。缺失内存/磁盘目前可变成 0%，必须防止当真实健康历史。
- `internal/controlstore/store.go:300–330`：主表按分钟 `INSERT OR IGNORE`，温度 `INSERT OR REPLACE`；没有事务，同分钟重复请求会保留最初主指标而替换温度，且失败时可能只写一部分。清理仅发生在写样本后。
- `MetricSamples` SQL 只有 since 无 to；HTTP `QueryMonitoringSamples` 在内存按 from/to 再过滤。主表一分钟主键限制常规七天约 10,080 点，但传感器数没有显式上限，异常未来时间也会留存。
- `controlstore.Open` 只有一个 SQL 连接，`busy_timeout=5000`。采样写入不能无界排队，也不能持快照 mutex 做 DB IO。
- `cmd/ncp-server/main.go:runHTTPServer` 已有 signal context、10 秒 shutdown、store 关闭和新 JobEngine 生命周期；应并入该 owner，不能在 `NewHandler` 隐式起永久 goroutine。现有数据库进程锁可避免同一数据库两个 serve 实例的重复 writer；不另建分布式锁。
- `frontend/src/stores/system.ts`：单标签 EventSource、60 个内存点、session generation、重复 timestamp 去重均可保留。当前 error 有时只写 errorCode，旧数据仍可经 `updateRefreshState` 被标 connected；无独立 freshness 时钟，流连接成功不代表数据新鲜。
- `frontend/src/api/systemInfo.ts` 的 SSE 事件名是 `snapshot`；`api/system.ts` 的 `RealtimeSnapshot` 为 collectedAt/summary/docker/errors。保留该外壳。
- Docker 的 `internal/docker/moby_gateway.go:Events` 和 `poc.go` 是单容器 PoC 验证；dashboard RPC 当前只有 unary summary/details/inventory。生产 inventory 不存在断线对账订阅，不能声称已具备。

## 3. 最小内部契约与文件归属

新增 `internal/monitoring/collector.go`、`snapshot.go`、对应 `_test.go`，总量以小型明确 owner 为目标，不做通用调度框架。

建议接口：

- `Source.CollectSystemSummary(context.Context) (system.Summary, error)`：不接受浏览器 context，不直接读取 /proc。`cmd/ncp-server` 增加小 adapter，封装 socketPath 和现有 RPC；包内不依赖 httpapi。
- `Store.RecordMetricSample(context.Context, controlstore.MetricSample) error`：沿用已有签名；同一分钟首次完整成功记录获胜，重复写必须整体无副作用。
- 新增 `Store.PruneMetricSamples(context.Context, time.Time) error`：截止时间来自服务时钟，原子删除主表及温度孤儿，独立于 RPC 成败。
- `NewCollector(source, store, options)`：验证 interval/timeout/retention，必须显式 source，测试不得 nil 后退到真实 gopsutil。
- `Run(ctx) error`：只允许启动一次，阻塞到退出，由 main 管一个 goroutine；`Done()` 用于等待。取消必须传到 RPC/SQL。不得为超时调用另起无法回收的 goroutine。
- `Latest() State`：返回拥有独立 slices 的快照，不能给调用者内部可变引用；包含 LastAttemptAt、LastSuccessAt、LastErrorCode、LastPersistedAt、PersistenceErrorCode、Summary。时间戳分别表示尝试/成功/样本，不用发送时间覆盖。
- B 阶段可增加 `Subscribe(ctx) (updates <-chan struct{}, unsubscribe func(), err error)`：每订阅者容量 1 的失效通知；消费者收到后读 Latest。通知非阻塞、允许合并、无历史重放队列。取消与广播统一锁保护，无 send-on-closed；unsubscribe 幂等。

`httpapi.Config` 注入最小只读 `SummarySnapshots` 接口；`NewHandler` 不创建 collector、不偷偷启动它。生产配置缺失时明确 unavailable，测试使用 fake snapshot provider。A 的过渡期允许原有 direct summary read，但无任何请求写历史；B 完成后删除该路径的 direct RPC fallback。

## 4. 采样与历史写入语义

1. 启动立即一次 RPC，之后 5 秒节拍；同一时刻至多一个 summary RPC。慢调用跳过错过的 tick，不追赶、不并行补采。
2. RPC deadline 5 秒；成功先发布内存快照，再尝试分钟持久化。A 可用一个顺序循环完成 RPC/一次短 SQL/维护，SQL deadline 建议 2 秒；该阻塞会让下一 tick 延迟，属于允许的有界退化，不冒充严格周期。
3. 按 `Summary.CollectedAt` 的 UTC 分钟计算桶，语义仍为该分钟首个合格快照，不是均值/最大值。校验非零时间、不能超服务 now 的允许偏差（建议 5 秒）、不能倒退或已过期；不把旧 Agent 返回当新采样。freshness 的运行时年龄优先使用服务接收时间的单调时钟，同时对 Agent timestamp 做校验。
4. 单桶待写缓存最多 1 个，保存第一次合格样本，失败后下个采样周期重试同一内容；成功才标记写入完成。跨分钟仍失败则丢弃旧 pending 并记录 gap/计数，保留当前分钟候选；不无限补发历史，不生成虚构数据。
5. 全部样本及其温度在同一 transaction 内提交。主表 INSERT OR IGNORE 后 RowsAffected=0 即不再修改温度；新主样本插入后全部温度成功才 commit。取消、磁盘满、sensor 写入失败都 rollback。
6. A 不修改历史 JSON schema：出现 cpu/memory/storage/network/disk-io 源失败 warning 时不写这次历史点，保留明确间断；host/temperature warning 不让其余真实主指标失效，温度只存已读到的子集。详细快照仍携带 warnings。这样避免将缺失值写成零，也避免立即迁移所有字段成 nullable。后续确需逐字段历史有效性再单独协议变更。
7. 温度先按名称去重、排序。建议最多 128 个、单名称 UTF-8 最多 256 字节；超过限额拒绝整个历史候选并报告 `MONITORING_SAMPLE_LIMIT_EXCEEDED`，不能默默截断后声称完整。限额是提议值，隔离大 fixture 验证后落定。
8. 所有 float 必须 finite；非有限值/异常计数器范围必须返回稳定无效样本错误，不让 JSON/SQLite 失败隐身。累计计数器保持原值，不在 Server 存速率。

## 5. 保留与资源预算

- 保持 7 天、一分钟一点；按 UTC minute cutoff 计算，含边界最多 10,081 主样本。启动清理一次，之后每分钟清理，无 Agent 数据也执行。过期 SQL 出错要暴露，不把失败当清理成功。
- 只按已有7天策略删除过期主记录及其温度、清理孤儿。不得因新传感器上限或未来时间而删除仍在保留范围内的历史桶；新写入严格限制，历史异常应诊断而不是静默截断/删除。10,081桶上限适用于合法新写入，并非对所有遗留数据的无条件保证。
- 每桶 128 温度下最多约 129 万温度行，是逻辑上限，不声称一定多少 MiB。SQLite 文件/WAL 高水位不因 DELETE 自动缩小，不能把行限额说成硬磁盘限额；不在每次采样 VACUUM。若实测文件预算不满足，再另定温度频率/保留，不先上压缩系统。
- 内存仅 last-good + 一个 pending bucket + 状态；不再维护服务内 7 天 ring。Summary 建议 1 MiB 编码上限，超限错误而非盲目保留。未来 Docker snapshot 单独最多 4 MiB；不写完整 inventory 到 SQLite。
- summary 基线至多 12 RPC/分钟，正常 1 次主事务/分钟及 1 次清理/分钟；重试上限每 5 秒一次（故障 12 次/分钟），可按持续失败指数退避 5/10/20/30 秒，成功重置。不要因多标签增加源采集次数。
- B 建议全局最多 64 条 system SSE，每条只存容量 1 的通知，不拷贝排队 payload。超过上限在写 header 前返回 503 `REALTIME_SUBSCRIBER_LIMIT`。JSON payload 共享不可变编码或按范围编码；不能仅限制 channel 而让慢连接阻塞写 goroutine。
- 每次 SSE Write/Flush 设置约 5 秒 write deadline（ResponseController，验证当前 wrapper 支持），写失败/取消即退订；heartbeat 15 秒确保中间代理与故障检测，不创建新 RPC。指标阈值为设计预算，尚无真实 NAS benchmark。

## 6. API 兼容、SSE 和过期显示（B）

沿用三个路径：

- `GET /api/v1/system/summary`：返回新鲜完整/带 warnings 的原 Summary JSON；冷启动未采到、最近尝试失败或超过 15 秒返回原稳定 503 `SYSTEM_SUMMARY_UNAVAILABLE`。不返回用当前时间重标的旧数据，也不按请求即时补采。前端保留本地上次成功数据并显示时间/错误。
- `GET /api/v1/monitoring/samples`：仍返回原数组，默认 6h、预设 1h/6h/24h/7d、显式 from/to 沿用现有解析。补充 store `MetricSamplesInRange(ctx, from, to)` 用 SQL 上下界与 LIMIT 10081，避免读到上界外所有记录；保留旧方法适配调用者，HTTP 使用新窄接口。所有 slices 包括温度受上述上限约束。
- `GET /api/v1/system/events`：保留 event:snapshot、retry、scope=summary/docker 和 interval=2..300。interval 改为消费者发送节流，不控制采集；2 秒请求可收到同一 collectedAt，现有前端去重应生效。新订阅立即获得现状，之后发最新可用状态，无 replay、不保证每个采样都送到。

最小兼容错误方案：summary 出错/过期时省略 summary 并在 errors 中加入 `SYSTEM_SUMMARY_UNAVAILABLE`；历史失败另外报 `MONITORING_WRITE_FAILED`，实时 summary 仍正常。只使用旧 fields 即可，但必须给前端错误映射和 OpenAPI 添加说明。可选新增 `status` 对象应留到确有展示需要，不能默默加未验证 envelope。

外层 collectedAt 是事件生成时间，内层 summary.collectedAt/Docker.collectedAt 才是数据时间；文档写清。前端不能因新外层时间、onopen 或 heartbeat 将旧数据标绿。`stores/system.ts` 修正 error 优先级，并用一个可停止的 freshness timer（页面作用域/session 关闭）独立检查数据年龄。summary 超 15 秒 degraded/无数据 unavailable；断线但有旧数据保留并明确“上次更新”。警告源不可显示为健康 0。

历史图表须按相邻时间间隔显示 gap；累计计数器倒退、非正时间差、长缺口不绘假零速度或跨缺口平滑连线。涉及 `frontend/src/views/MonitoringView.vue`、`domain/monitoring/series.ts` 和相关图表逻辑，隔离验证中区分 warm-up、gap、counter reset，不能仅凭 summary 去重测试宣称覆盖。

## 7. 生命周期与失败

- `runHTTPServer` 打开 store、构造 Collector、成功取得 listener 后启动 Run；即使没有认证用户/浏览器也持续采样。未初始化登录不改变后台采样职责，不把数据暴露到未认证 API。
- 所有退出分支，包括 Serve 意外错误，必须 cancel collector 并 wait，再关闭 SQLite；取消 streams 后再 HTTP Shutdown，防止长 SSE 占满现有 10 秒窗口。与 JobEngine 共用有限 shutdown budget，不能每个 owner 顺序再等 10 秒。
- Collector stop 拒绝新订阅、取消 RPC/SQL、退出 ticker、关闭已有 stream 的 done；不强制在退出时用无限独立 context 补写 pending。未写成功的尾点允许丢失且可观测，历史不是审计日志。
- Agent 暂时断线：保留 last-good 及其原时间、标 source error，有限退避后自动尝试；恢复成功立刻发布，历史不会填补停机期间。
- 存储失败不终止 HTTP 或覆盖有效实时快照；暴露稳定状态/日志（无宿主机详细错误或凭据），日志按状态变化及每分钟聚合，统计 source_errors、write_errors、prune_errors、skipped_ticks、subscriber_drops。先用现有 slog，不加新的监控平台。
- 服务重启不从 SQLite 分钟记录伪造实时 Summary，冷启动 503 到首次成功；读取历史正常。系统时间后退时不覆盖旧桶，标时钟异常/跳过持久化直到合法时间；无需试图自动修主机时钟。

## 8. Docker 共享与事件后续（C）

先将 `CollectDockerInventory` 放独立 service-owned 单 worker，最多每 5 秒一个请求，仅有 Docker 订阅时启用；停止订阅后取消/空闲停止。summary 采集完全不等 Docker。手动 inventory 读取/操作后刷新要进入同一 coalesced refresh 通道，确保操作结果不被旧缓存覆盖；命令成功不等同于刷新已成功。

再在 Agent 引入强类型 inventory invalidation stream（新 dashboard/专用 RPC，明确与 PoC 分离），Server 订阅只有一条。事件仅为“应刷新”提示，不作为完整事实，不把原始 Docker Event 日志放入 SQLite。

- 首次连接建立订阅后执行完整 inventory 对账；对账期间事件置 dirty，一轮完成后最多再执行一轮，防止 list/subscribe 缝隙丢状态。
- EOF/error/overflow 统一置 degraded + dirty，重连使用有界 backoff；每次重连必须 full inventory reconcile，不能只从最近事件继续。
- 一个 worker、容量 1 dirty 标记、事件 burst 合并（建议 250ms），限速避免 event storm 循环 list；定期低频全量对账兜底，保留 Engine 为事实来源。
- socket restart、事件丢失、容器删除、外部 Compose 变更都用 fake event source 与 inventory provider 验证。实现前单独确认 schema 与 API 兼容；当前方案不修改 Agent/Docker 生产行为。

## 9. 可单独落地的首切片 A

精确修改面：

1. 新增 `internal/monitoring/collector.go` 与 fake-clock/source/store tests，只负责持续 summary → minute sample、状态、清理、退出。
2. `internal/controlstore/store.go`：RecordMetricSample 改原子“主/子同时 first-wins”，加入 PruneMetricSamples，保留表与 JSON；新增 `metrics_test.go` 使用临时 SQLite。
3. `cmd/ncp-server/main.go`：显式 new/run/cancel/wait，adapter 调现有 agentsocket；复用 JobEngine shutdown，修改需与 M06 owner 协调。
4. `internal/httpapi/handler.go`：删除 GET summary 和 SSE 内 `recordMetricSample` 调用及 helper；A 暂保留其读取 RPC，明确不完成 fanout。handler.go 当前 M10 正在修改，需协调而非覆盖。
5. `api/openapi/openapi.yaml`：说明历史由后台采集，一分钟首合格快照、7 天保留及间断语义；A 不改响应 shape。

首切片隔离验收（全部 fake，不能顺手运行有真实 provider 的全包集成测试）：

- 不构造任何 HTTP 请求，手动 fake clock 推进 3 分钟，观察持续 source 调用及 3/4 个分钟桶（按初始边界明确断言）。
- 100 次 GET/多条旧 SSE 读取不再调用 store 写；A 中额外 source RPC 是已知未消重，不能把它写成通过 B。
- 同一桶 A/B 写入主指标和温度：只保留 A 全体；温度故障/取消事务 rollback，无 orphan；重试 A 幂等。
- RPC error、全部/部分 warning、零/未来/倒退时间、NaN、传感器超限不造健康历史；温度空保持空。
- store 失败重试同一 pending，跨桶丢弃有界，取消阻塞 source/store 后 Run 退出，Close 前无存活 writer。
- fake clock 经过 8 天且 source 永久失败，旧记录仍被清理；重复 prune 幂等，模拟失败恢复后收敛到行数预算。
- Latest 返回对象修改不会污染内部 snapshot；并发 Latest/Run/Stop 跑 race。

建议命令：新增隔离测试以 `TestMetrics...` 统一命名；`go test ./internal/monitoring ./internal/controlstore -run '^TestMetrics'` 与 `-race`。HTTP/main 新 lifecycle 测试也选精确 fake-only 名称执行。编译用 `go test ./internal/monitoring ./internal/controlstore ./internal/httpapi ./cmd/ncp-server -run '^$'`，仅编译不运行既有真实环境测试。正常 Go build / OpenAPI 检查按项目既有最小门槛执行；需确认命令不触发实机能力。

B 额外验收：N=0/1/64 SSE 下每 tick 源计数不变；慢读不阻塞快订阅；64上限；取消回收；2/5/300 interval 只改发送；冷启动/中断/恢复/最后成功变旧状态；logout清理前端 timer；真实 Chromium 使用 fixture 验证过期文案及图表 gap。会话撤销后断开后端流归 M08，M13 保留 request context/owner cancellation 接口，不声称替代会话撤销。

## 10. 完成定义与未验证项

A 完成 = 无浏览器仍产生一致、有界、可清理历史，错误可见、生命周期可关闭。B 完成 = summary 全部读取路径复用同一采集快照、N 个页面不增加主机指标采集、过期数据不显健康。C 完成 = Docker 单流/单刷新及每次断线强制对账。

本文是设计交付，没有执行上述测试，没有采集真实机器指标，没有性能实测，没有对 NAS 部署或变更。Docker C、M08 会话撤销、逐字段历史质量迁移不应被 A/B 的通过状态掩盖。

## 10. A实施记录与未完成边界

- 新增 internal/monitoring.Collector，由serve显式启动；5秒采样/RPC预算、2秒SQL预算、15秒时间戳年龄与5秒未来容差。一个最新深复制快照、一个待写分钟桶、独立启动/每分钟清理。
- 主指标及温度以单事务首次完整分钟获胜；拒绝非有限数、计数器溢出及超限新传感器，保留仍在窗口内的旧版异常桶，不新增无条件删除或VACUUM。
- HTTP/SSE已移除历史写入；后台采集不依赖任何页面。A仍保留HTTP/SSE直接源读取，不能宣称多标签RPC已消重。
- 共享10秒关闭预算，先取消owner上下文，再并行等待HTTP/Job/Collector；正常RPC/SQL须响应context。超时不能强制结束不合作的外部实现，不能宣称硬杀保证。
- Fake时钟、故障Source、临时SQLite的四包TestMetrics race通过，root另跑3轮；无页面3分钟、8天源故障清理、并发快照隔离、原子失败重试及100次HTTP/5次SSE零写入均覆盖。go vet ./...通过。
- SQL上下界/LIMIT、共享summary/SSE、实时过期UI、Docker事件与断线对账尚未实现；真实NAS与性能基准未执行。旧版MetricSamples仍是since-only查询，后续B改造不能静默裁切温度却声称数据完整。

## 11. B实施收敛决定

B1先完成GET/summary SSE共享A的Latest、15秒接收/数据双年龄判断、64连接名额、5秒写入/flush预算与15秒注释心跳。保留每连接ticker读取缓存，不新增Subscribe队列；interval仅为发送节拍，不触发summary RPC。Docker仍直读，混合流发送前再读summary以避免其等待导致陈旧发送。responseRecorder增加可传播错误的FlushError。

前端统一store、Overview与AppShell的陈旧/部分源失败状态，onopen/外层时间/重复数据不得保鲜；停止MonitoringView将实时秒级summary灌回持久化分钟历史，避免补造A故意留下的缺口。历史写失败与实时源不可用分别说明。

B2单独完成SQL上下界及limit+1检测、明确响应预算与完整图表gap。旧版超限温度集合不得静默截断或删除，超过响应预算应返回稳定显式错误并提示缩小范围。Docker共享/事件对账、会话撤销传播均不在B1完成声明内。

## 12. C0实施契约：先共享Docker SSE

2026-10-13开始此切片。只共享Docker SSE的采集，不改GET /docker/inventory、services、sites的新鲜unary读取，避免把操作前缓存当作操作后核对成功。GET与SSE源调用仍可重叠；C0不代表全路径单刷新或事件断线对账完成。

- 增加显式服务生命周期owner，source必须注入并经Agent RPC；非特权Server不直接访问Docker。无Docker SSE需求时不采集；首次需求立即采集；真实源调用启动间隔至少5秒，零需求后快速重订阅也遵守该间隔，避免反复打开页面绕过节流。源调用预算5秒。SSE interval只控制发送。
- 最多一个SSE采集worker和一个latest状态，无无界事件/响应队列。最后订阅释放取消在途采集与周期工作，重新激活不得与未退出的旧调用重叠，旧代迟到结果不得发布。owner退出接入现有共享shutdown预算。
- Latest深复制containers、ports、labels、time指针、projects/configfiles。接收年龄与原始CollectedAt双重判断，源失败/重复或倒退时间不能重标健康；保持15秒过期约定，心跳不延长数据寿命。
- SSE只读取快照，不等待Docker RPC；慢Docker不阻塞summary/heartbeat。复用64连接名额、逐帧写预算、会话撤销和request取消；缺provider或冷启动明确不可用，不静默退回每连接RPC。
- 不静默裁剪容器清单；明确缓存/编码容量和错误。容量限制不等于Docker驱动采集过程峰值内存硬上限，不提高全局gRPC限制掩盖超大结果。
- 仅用fake/source barrier、内存RPC验证0/1/64需求、共享计数、混合流、取消/重新订阅竞争、旧代结果、深复制、容量与真实时间戳。真实Docker/NAS未授权测试。

后续C1才统一所有读取的强刷新屏障，要求操作后的Refresh不能被操作前已经开始的采集满足；C2才添加Agent失效通知流与每次重连全量对账。现有Docker事件PoC不能冒充生产事件订阅。SDK错误流关闭/EOF与重连由调用方处理，事件通知只表示需要刷新，不能直接作为完整清单或可靠审计日志。
