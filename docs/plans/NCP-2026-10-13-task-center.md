# M15-B：最近任务中心首切片方案

日期：2026-10-13。静态检查基线：`cadd8b8192bb57951b7c009f26bda8c02d53b7da`。方案源于静态审查；实施基线为 e1230e7。生命周期、UI和浏览器fixture分工推进，完整验收尚未完成。

## 1. 真实缺口与现有证据

- `frontend/src/composables/useComposeWorkspace.ts:publish` 只在当前编辑器会话内观察部署 Job；关闭/切换后不能从一个通用入口重新发现结果。`DockerImagePanel.vue` 和 `useDockerImageDownloads.ts` 仅列出镜像拉取任务。
- `internal/httpapi/jobs.go:listJobs` 已支持不带 type 获取混合任务；`compose.go` 使用 `compose-deploy`，reference 为项目 ID；镜像为 `docker-image-pull`。不需要新任务协议或新后端资源。
- `job_engine.go:list` 返回内存 registry 快照，启动读取最近 500 条，`trimHistory` 保留活动 owner 并尝试裁剪终态。它不是完整数据库历史、不是严格 500 条上限，也没有服务端游标/total。按 ID GET 能读数据库不等于 list 完整。
- 通用列表含已完成镜像时，`resolveDockerArtifactStates` 会做最多 5 秒的 Agent 镜像清单查询。避免全局常驻高频轮询。
- `api/docker.ts` 已有 requestJobs、cancelJob、subscribeJob 和六状态 parser；SSE 出错即关闭，当前 image composable 静默吞掉列表及流失败，不宜直接改名作为通用任务中心。
- `useDockerImageDownloads.downloadState` 将 completed + artifact unknown + 缺少本地匹配视为 deleted。通用任务中心不能复制此映射：任务完成与产物当前存在性是两件事。
- `AppShell.vue` 已有顶栏、移动导航焦点/inert；`router/navigation.ts` 同时参与用户导航排序。顶栏入口能避免新增一级导航及偏好迁移。

## 2. 有界产品范围（B1）

在登录后顶栏增加带 ListChecks 图标的“任务”按钮，打开“最近任务”抽屉；不新增 sidebar 项、路由、后台通知或未核实的活动数量徽标。抽屉随关闭卸载，保留原页面/编辑草稿；从任何页面均可主动查看。

首片交付：
1. 混合最近列表，按创建时间倒序（ID 稳定次排序）；本地类型/状态/关键词筛选及分页（默认 20）；计数写“当前列表”，搜索提示范围为“已加载记录”。类型提供镜像拉取、Compose 部署及未知类型的原始标签。
2. 每行展示对象/reference、类型、明确状态、进度、更新时间；可展开单条详情展示完整 ID、创建/完成时间、message/error/errorCode。长文本折行，按纯文本渲染。
3. 手动刷新，打开时读取；抽屉打开且文档可见时低频串行刷新，见下节。后台隐藏、关闭、登出立即停止观察，不暗示停止后端执行。
4. 仅镜像任务、明确 cancellable=true 且状态允许时提供带文字“请求取消”的动作。Compose 或未知类型只读，显示“此任务不支持取消”。接收 202 后使用服务端快照，绝不乐观写 cancelled。
5. 持续说明：“显示控制面当前可提供的最近记录，可能不包含更早历史。关闭面板不会停止任务。”

不纳入 B1：重试、恢复、重放、删除/批量清理、保留期限配置、任务创建表单、全局通知、终端多会话、历史全量分页及新的 Job 类型。现有镜像页面操作继续保留；不在首片抽取/重写它。避免把操作范围扩大到 Compose 取消/重试或生成不成立的资源深链。

## 3. 读取、竞争与容量

### 单一观察生命周期

- 新建 `useTaskCenter`，组件挂载时 captureSession；复用 `domain/requestSequence.ts` 的 latest-wins 思路/适合的接口。抽屉每次打开是新观察代次，所有 list/detail/cancel 回调都验证 session、代次和存活状态，包括 finally。
- 首片采用列表轮询，不为每条活动 Job 开 SSE。打开立即读，成功后间隔 15 秒安排下一次；只允许一条 list 在途。关闭/隐藏清理 timer；重新可见立即校准一次。手动刷新与自动刷新共用门闩，不积累请求，不使用 setInterval 重叠。
- 失败后保留数据，显示刷新失败、上次成功获取时间；按 30/60 秒上限退避，手动重试可即时触发。文案是“定期刷新”，不称实时流。刷新失败时保守禁用取消，成功读取再恢复。
- `requestJobs` 首片保持现有 API 签名即可，用会话/代次隔离迟到读取；若添加 AbortSignal，仅做可选兼容参数并保留现有 fetcher 测试入口，不把物理取消 HTTP 当作后端 Job 取消。
- 不订阅列表中的所有记录，因此额外 SSE 连接数为 0；关闭抽屉后额外 HTTP 轮询数为 0。对活动任务也只显示最近采样状态，不能展示假“连接正常”状态。

### 取消和列表竞争

- per-ID in-flight Set 防双击；只允许 type=docker-image-pull、cancellable=true、queued/running、无 persistence pending、无 cancellation requested、当前数据非 stale。
- 发起取消前冻结 ID 和对象标签，按钮禁用并显示“正在提交”；409/404/503 使用稳定 NcpApiError.code，保留记录及错误，触发一次合并后的校准刷新。404 表示无法再读取，不推断资源删除。
- list 发起时记录本地 mutation version；取消快照提升该 ID version，早于该写入发起的 list 不得覆盖它。当前已知终态不得被旧活动态/不同终态倒退；不能仅依赖秒级 updatedAt 判断新旧。
- pending/requested 等标记优先级与 M06 一致；取消响应若 completed 应显示真实完成，不能提示“已取消”。请求发出后关闭抽屉/登出不能撤回操作，迟到结果不能写入新会话或弹通知。
- 新一轮成功 list 作为“当前可提供列表”的依据；遗漏的记录不解释成任务已删除。不要无限累积缺席任务或墓碑，把前端当成完整历史库。

## 4. 状态语义

- persistenceStatus=pending → “结果保存中”，无完成承诺，禁止操作。
- 活动态 + cancellationStatus=requested → “取消请求已接收”，辅助说明远端可能仍执行。
- interrupted 或 cancellationStatus=unknown → “结果未确认”，提示检查实际资源；不提供恢复/重放按钮。
- queued/running/completed/failed/cancelled → 排队中/执行中/已完成/失败/已取消。Compose 成功只表明 Job 命令完成，不替代配置/运行状态核验。
- artifactState 独立作为镜像详情：“存在 / 已不存在 / 未核实”；unknown 永远不等于 deleted，且不覆盖 completed 状态。
- 首次读取：ResourceState loading；首次失败：error + 重试；成功空集合：尚无可展示记录；筛选无匹配：清空筛选；刷新中/刷新失败：保留旧列表并加可感知状态条。
- invalidCount>0 时说明“有 N 条记录无法解析”；全无有效记录且 invalidCount>0 显示“记录无法读取”，绝不称没有任务。
- 查询结果的 lastFetchedAt 是客户端取得快照时间，Job.updatedAt 是任务更新时间；两者分开。长期排队/静默运行本身不能推断断线或失败。

## 5. UI/可访问性

- 使用现有 ElDrawer，桌面宽 `min(760px, 100%)`，窄屏全宽；固定简洁头部与筛选，列表独立滚动，短高屏允许工具栏换行。单列任务卡/紧凑行，不引入大仪表盘或装饰统计卡。
- 直接消费 `styles/tokens.css` 的 surface/line/text/primary 与语义状态色、radius、duration；只写组件 scoped CSS，不改全局 token。数字使用 tabular-nums、ID 使用 mono，中文字号沿用现有辅助文字下限。
- ListChecks/RefreshCw/ChevronDown/CircleAlert 等现有 Lucide 库图标，状态有文字而非颜色独占。所有动作触控区域至少 44px，纯图标按钮 aria-label，输入有真实 label。
- 标题关联 dialog；打开焦点到关闭按钮或标题、Escape 关闭、Tab 不逃出、关闭恢复顶栏触发按钮。ElDrawer 内建行为须实测，不能仅因使用组件就声称符合要求。处理移动菜单/抽屉互斥及弹层焦点，不覆盖已有 inert 规则。
- 不每 15 秒重建全部行或把焦点移走；对状态变化使用精简 polite 文案，避免整列表反复读出。进度条提供可访问名称及数值；未知进度不用伪造百分比。
- 尊重 reduced-motion；200% 字体、390/768/1280/1440 宽及短高 viewport 无横向整体溢出；完整 reference/message 在详情可读，不靠 hover。

## 6. 精确改动点

新增：
- `frontend/src/components/TaskCenterDrawer.vue`：抽屉、筛选、分页、行内详情、ResourceState；复用 ResourcePagination。
- `frontend/src/composables/useTaskCenter.ts`：最近列表、会话/可见性/刷新生命周期、取消命令与竞态。
- `frontend/src/domain/jobPresentation.ts`：通用纯状态/能力/排序辅助函数；明确区别于镜像产物映射。
- 对应 `TaskCenterDrawer.test.ts`、`useTaskCenter.test.ts`、`jobPresentation.test.ts`；`frontend/e2e/task-center.spec.ts`。

修改：
- `frontend/src/layout/AppShell.vue`：一个顶栏按钮、打开状态与按需挂载抽屉；尽量异步加载组件以控制初始包。
- `frontend/src/layout/AppShell.navigation.test.ts` 或新增 `AppShell.tasks.test.ts`：移动菜单/任务入口焦点及断点回归。
- 仅确需请求取消才扩展 `frontend/src/api/docker.ts` 的可选信号及其测试；无需挪动所有 Job API 到新文件。
- 后续由主任务合并 `docs/plans/NCP-2026-10-13-roadmap.md` 中 M15-B 状态和本方案。不能覆盖主任务正在维护的 roadmap。

不修改：Go/DB/OpenAPI（无协议变化）、router/navigation、Compose 编辑事务、旧 image composable、terminal/C2。若实施发现 API 必须改变，应回报并拆出新边界，不能静默扩张首片。

## 7. 验证与退出条件（待实施，非已通过）

单元/组件：
- 混合/未知类型、六状态及 pending/requested/unknown 优先级；artifact unknown 与 completed 分离；筛选/分页归位、长中文对象。
- 首屏 loading/error/empty、旧数据刷新失败、部分/全部无效记录；不能静默失败或把解析异常当真空态。
- A 慢 B 快；取消响应比 list 新；取消完成与终态先后；同 ID 重复点击；409/404/503；关闭再开、登出换会话、卸载后 finally 无权改新 busy/提示。
- fake timers 证明串行轮询、15 秒成功节拍、失败退避、隐藏/关闭清理、恢复可见仅一次立即读取、无每行 SSE。

隔离浏览器 fixtures：
- 从总览及 Docker 打开中心，Compose 记录可见；原页面状态及未保存编辑不被清空；关闭不会发送 cancel。
- 键盘完整打开/筛选/展开/取消/关闭，Escape/焦点恢复、移动菜单互斥；axe；长对象、长错误、200% 字体与 reduced-motion。
- 按上述 viewport 截图，记录精确 SHA、fixture 来源；实际看图确认顶栏新增按钮不挤压现有健康/刷新区。无本地 Chromium 时依既有开发分支 CI 流程，明确尚未完成的截图验收，不调用真实 NAS。

命令建议：frontend 目录执行 `pnpm test`、`pnpm lint`、`pnpm typecheck`、`pnpm build`；CI 跑新 task-center e2e 加现有 job-lifecycle/navigation/Compose 关键回归。保留现有 bundle budget。无后端修改不虚报 Go/API 验证。

退出条件：跨页面可重新发现这两种已有 Job 的最近状态；状态过期/异常/取消未知均有明确表达；隐藏/关闭无后台轮询泄漏；不倒退终态；窄屏和键盘通过实际 fixture 验证。完成后再决定 B2 是否值得做受控重试、单条实时详情或后端历史游标，各自单独立项。

## 8. 实施记录

2026-10-13 14:56 UTC：按方案完成顶栏异步入口、最近任务抽屉、纯状态呈现和观察生命周期。只在抽屉可见期间串行轮询，关闭立即停止观察，正常离场动效结束后卸载；会话失效立即清理。取消回应校验ID/类型/对象，迟到列表与旧finally不能覆盖新操作。

- 55文件652项本地单元测试通过，lint、类型检查、生产构建及540KiB包预算通过；独立生命周期/界面源码审查完成。
- 浏览器fixture覆盖混合记录、取消202、过滤、无效记录、隐藏和关闭、模态键盘、移动导航互斥、四宽度100/200%根字号；实际Chromium、axe与截图检查仍待CI。根字号压力不是浏览器真实zoom。
- 审查发现减少动态效果的全局.01ms规则覆盖组件transition:none，fixture按实际全局契约校验，不能断言精确0s。
- 已打开Compose编辑器本身是模态，任务入口在其下层不可操作；不强行穿透遮罩制造“保留编辑器”验收。验证普通Docker页面搜索状态不因抽屉打开/关闭重建，Compose编辑事务沿用已有独立回归。

### 首轮浏览器失败与修复方向

f06daee的CI 38417962795：181项通过、11项失败，原有173项均通过。新fixture发现抽屉默认header成为第二个banner、取消禁用按钮后Tab焦点离开模态、select精确标签受option文字干扰；另有测试时钟暂停到过去的问题。八组详情截图在axe失败前未生成，不能宣称详情视觉通过。

修复保持实际语义：自绘div标题并关联dialog，过滤控件明确关联标签文本，取消前保持同一行可用焦点，记录消失时回到关闭按钮；短高视口整段滚动并固定关闭入口。fixture保持全部断言，等待动画结束后再一起采集几何与PNG，补充真实滚动和控件命中验证。原始失败与17张PNG复核见[视觉报告](../qa/NCP-2026-10-13-task-center-visual.md)。

## 9. 镜像下载原入口：读取与进度中断恢复（2026-10-13 实施中）

源码基线 `d5108ea`：通用任务中心已经单独交付，但原“Docker → 镜像 → 下载任务”仍吞掉首次列表错误与 SSE 中断。首次失败可能显示无匹配记录，中断后旧速率和“下载中”可能一直像实时数据。此片回到第 1 节已记录的旧入口问题，不重写通用任务中心或新增任务类型。

- 列表显式区分首次加载、首次失败、成功空列表、筛选无匹配、旧列表刷新失败；失败保留已有快照，并提供针对性只读重试。
- 每个 Job 的观察中断独立于执行状态。断开不把任务改成失败、完成或已取消；提示当前为上次观察，隐藏无法确认实时性的速率，不声称远端停止。
- 原刷新入口合并并发读取；取得当前快照后只为仍活动的任务恢复单条进度流。读取或重连不得重新拉取镜像、自动调用 retry/cancel/delete，也不新增后台轮询。
- 保留已有终态单调、SSE/HTTP 版本隔离、删除墓碑和会话失效清理。旧请求、旧流结束与旧 finally 不能影响新观察；刷新失败不关闭仍正常的流。
- 仅改下载 composable、原面板及聚焦测试。验收使用可控 Promise / fake stream：首次和缓存失败、断流、终态与活动恢复、同时刷新、更新竞争、退出/卸载后的迟到结果，以及没有变更请求重放。
- 前端静态与单元结果、实际 Chromium/axe/窄屏截图分别记录；不访问真实 Docker/NAS，不修改 Agent/API 或部署。

### 下载观察恢复实现与隔离验证

首轮四文件实现通过后，独立审查发现 Hub 新建任务仍绕过下载观察者：旧 Hub 单独持有 SSE，导致新任务既不展示可信速度，也无法在断流时显示恢复入口。已把进度观察统一交给下载 composable，Hub 只提交并发布创建结果；面板立即登记新任务，不多发列表请求，不增加第二条 SSE。仅本次会话创建的任务在确认终态后通知一次并刷新本地镜像，历史记录读取保持安静，持久化未完成不提前通知完成。回调失败被捕获，退出后不再通知；创建跟踪在终态、删除或卸载清理。

41 项聚焦测试覆盖真实 Hub 组件与真实 composable 的创建→进度→断开→手动读取恢复路径，以及面板绑定和原有竞争守卫。主任务完整 74 文件 / 1322 项单元、全量 lint、类型检查、生产构建及包预算通过；独立复验 41 项通过。实际 Chromium、axe 与390/1440截图仍待新fixture CI。组件卸载会关闭流并拒绝迟到列表结果；列表 HTTP 仍沿用已有会话绑定传输，不能声称组件卸载会中止该 HTTP 请求。

### 窄屏恢复上下文（20:26 UTC，接续实际截图发现）

后代 a0e8c73 的 CI 38460901709 已通过 1337 项单元及 303 项浏览器，六张下载 PNG、六份状态与完整 axe JSON 均已复核，axe 为零违规。但 390px 下表格内部仍宽 960px：横向移到恢复按钮时，镜像身份、状态和中断说明离开视口；停在左侧时按钮又在屏外。可滚动到按钮不等于恢复上下文可同时阅读。

本片保持桌面列表与同一份数据/事件入口，提供窄屏下无需横向切换的任务身份、执行状态、观察中断说明与只读恢复动作。不得复制 SSE、增加写请求、把断流当成任务失败或隐藏原恢复边界。用组件测试检查绑定和操作不重放，实际 390/1440 PNG 与几何核验上下文和按钮同时可见，再决定视觉验收。

### 窄屏恢复布局实现（20:37 UTC）

同一任务DOM在下载面板实际宽度不超过980px或61.25rem时切成卡片；固定像素条件保护列宽，字体相对条件保护200%文字。身份、状态、中断原因、进度、元数据与恢复入口保持同组，长文本换行，操作显示文字并提供44px最小高度；桌面空间足够时保留原六列，未复制任务、订阅或写入逻辑。

独立审查确认单一观察者和操作守卫保留，并发现仅980px条件漏掉大字号列裁切，已补字体相对条件与八档200%强制卡片/完整文字断言。45项独立聚焦与主任务精确快照78文件/1428项单元、lint、类型检查、构建/包预算通过。20项浏览器用例已通过静态检查与发现，含八档宽度×正常/200%及长引用，实际PNG、axe、内部边界与恢复交互待CI，不把源码规则当成像素验收。

### 镜像工具栏宽度与字号恢复（21:08 UTC）

338b3b 的 34 张实际下载截图证明卡片内容可读，但 821/1024/1200/1280 等宽度仍出现外层来源标签裁切。原因是工具栏按视口 820px 切换，未反映侧栏占用后的面板空间与 200% 字号；父组件普通 scoped 后代选择器也未命中 SectionTabs 内部按钮。

本片只改变镜像面板样式，模板、脚本、下载观察与操作处理保持不变。工具栏使用独立具名容器，以 980px 或 61.25rem 为阈值，来源按钮、计数、搜索、状态筛选与刷新允许完整换行；深层选择器覆盖真实子组件，窄空间控件最小高度 44px。原下载卡片容器条件保留。

首次全量验证暴露旧 SectionTabs 测试硬编码六个原始选择器，1501 项通过、1 项失败。已改为挂载真实父子组件并检查编译后 scoped/deep 选择器及命中数量，保留键盘与选择行为断言，没有跳过测试。独立复核 32 项聚焦通过；以 f633432 为基线的最终精确快照 83 文件 / 1604 项单元、全量 lint、类型检查、生产构建与 540 KiB 包预算通过。20 项既有下载浏览器用例增加完整工具栏边界、文字范围、无隐藏横向滚动和不重叠断言；实际 Chromium 与 PNG 验收待 CI。该几何覆盖下载模式，不代表本地镜像批量工具栏及所有筛选值已完成像素验收。
