# NCP Vue 前端静态审查（待补本轮浏览器证据）

日期：2026-10-13。范围：`ncp/frontend`。已先阅读 `ncp/AGENTS.md`，未读取/启动 OpenSpec 工作流，未修改仓库、安装依赖、启动服务、运行测试或执行 NAS 操作。文中路径均相对 `ncp/frontend`。

## 结论与证据边界

这不是完成后的视觉审计。本稿是代码结构、可交互状态与异步生命周期审查，供主线程叠加本轮截图。没有把仓库内历史 Playwright 图片当成本轮证据，也没有声称类型检查、构建或测试通过。颜色实际对比度、字体最终落地、目标尺寸和溢出、读屏实际输出、模态焦点和真实请求时序均需要后续验证。

基础架构已经具备一致设计系统的骨架：语义 token、统一 WorkspaceHeader、Button/Select/分页/状态组件，系统会话取消，数据库表页请求隔离，日志 SSE 和终端资源回收。优先工作不宜是大面积换皮，应先消除“标题与数据错对象”“未保存却标已保存”“筛选后加载卡住”等行为风险，再收敛状态与响应式体验。

## 路由、页面与产品边界（全部路由已覆盖）

路由定义：`src/router/index.ts:6–86`；十个主入口定义：`src/router/navigation.ts:22–32`。所有业务页采用 lazy import；认证通过 App 外层切换，Login 不是独立路由。

| 入口 | 目的与实现 | 静态状态判断 / 应补截图 |
|---|---|---|
| 启动、初始化、登录 | `App.vue` 外层 checking/unauthenticated/authenticated；`LoginView.vue:20–49,65–120` | 有离线重连、表单错误、提交锁；初始化文案“唯一 Root”与多等权账号语义冲突。需首启、登录失败、服务离线截图 |
| `/` 总览 | `OverviewView.vue:76–159`：收藏入口、4 指标、3 趋势、磁盘 | 有等待存储、无收藏提示；收藏加载失败和空目录混用，见 F08。需完整、首次加载、局部数据失败 |
| `/sites` 站点管理 | `ServicesView.vue:355–402`：状态/分类筛选、入口探测、列表、资料编辑、忽略恢复 | 错误重试和探测 partial/unavailable 已实现；保存后同步失败反馈不一致。`/services` 重定向这里 |
| `/docker` | `DockerView.vue:383` 起：项目/容器/镜像子视图；详情、Compose、创建容器、Hub、下载任务 | 子组件边界已有；容器详情和 Compose 异步风险高。需每个子视图与抽屉 |
| `/system` | `InfrastructureView.vue:357` 起及 Infrastructure*Panel：概览、网络、存储、服务、监听、DNS/代理能力 | 请求序号已有，DNS 预览/确认/回滚状态明确；卸载只注销手动刷新。需能力不可用/部分失败和窄屏长资源名 |
| `/databases` | `DatabaseView.vue:171–214,237`：发现、项目分组/归档、连接状态 | 统一 DatabaseErrorPanel；依赖数据库 store session guard。需发现失败、空组、无搜索结果 |
| `/databases/:sourceId` | `DatabaseDetailView.vue:50–170,259–361`：连接、凭据、诊断、表目录 | 路由切换缺隔离；初次发现时可能先呈现“不存在”。需慢加载、凭据错误、来源 A→B |
| `/databases/:sourceId/tables/:table` | `DatabaseTableView.vue:150–236,267–385,485–634`：数据/SQL、分页排序、编辑/删除 | 路由、行、SQL、mutation 各有 sequence+abort，是较好参考。需宽表、主键缺失、长值、SQL危险确认、取消 |
| `/monitoring` | `MonitoringView.vue:120–143,279–331`：时间范围、6趋势指标 | 有骨架、错误重试、无样本；刷新清空旧样本，与 stale-while-refresh 体验不一致 |
| `/logs` | `LogsView.vue:62–195,237–282`：多来源、查询、历史 cursor、follow SSE | 有查询身份、取消、上限500、连接状态；加载更多中改筛选会卡 busy，历史与实时并行也需验证 |
| `/terminal` | `TerminalView.vue:114–155,249–303,333–363`：Root PTY/容器终端、能力协商、粘贴保护 | 显式关闭 socket、observer、timer、terminal；需真实 handshake/断线/移动键盘，不得审计时执行命令 |
| `/users` | `UsersView.vue:47–174,179–208`：等权 Root、密码规则、创建/重置/禁用/删除 | 有 loading/error/empty 和危险确认；请求无取消/序号、Promise.all 使密码规则失败拖累账号列表。需只读检查和取消确认 |
| `/settings` | `SettingsView.vue:76–129,218–228`：自动保存、字体、密度、侧栏顺序、恢复默认 | revision防止旧保存回滚编辑；离开 flush。需加载偏好与编辑并发、保存失败、字体加载失败 |
| 未知 URL | `router/index.ts:84–85` 直接回首页 | 找不到资源原因丢失；可低优先级提供明确404/返回入口 |

## 优先级问题清单

P1 指用户操作对象或数据正确性，P2 指可达性、恢复与状态可信度，P3 指一致性和维护性。均为静态发现/推导，尚未本轮浏览器复现。

### F01 / P1：Compose 保存响应可以把未持久化的编辑标为已保存
- 证据：`components/ComposeEditorDrawer.vue:77–85` 提交时序列化 file.content，await 后却写 `savedContent[file.path] = file.content`；编辑器在保存中仍可用（212），dirty 只比较当前文件（35）。
- 情景：文本 A 点保存，请求未回时改成 B；A 请求成功后，savedContent被赋 B，dirty=false，但服务端只有 A。
- 建议：捕获不可变提交快照，成功只标记该快照；新增编辑保持 dirty；“全部文件未保存数”与当前文件区分。
- 验收：延迟保存响应，A→B 编辑后显示未保存；再次保存请求是 B；失败不清 dirty。

### F02 / P1：Compose 配置读取/校验/部署缺少对象和修订绑定
- 证据：`ComposeEditorDrawer.vue:49–74` 多段 await 期间重复读取 props.project，未 capture projectId/sequence；189仅监听open；94–102校验结果可在编辑之后回来变成“配置有效”；112–129校验后弹确认，随后提交可变 file.content 与当前 props.project。
- 风险：关闭再开另一项目，旧响应覆盖；编辑修订变化后复用旧校验成功；确认摘要与实际部署内容不一致。
- 建议：所有读写绑定 projectId、path、revision，确认使用冻结 payload；切换/关闭终止前端观察并隔离旧响应。取消前端请求不等于取消服务端 Job，必须显示真实任务状态。
- 验收：A慢B快只见B；校验中编辑立即使旧结果失效；确认后发送的对象/文本与预览完全相同；运行Job不能因关闭抽屉被误报取消。

### F03 / P1：数据库详情凭据草稿和诊断会跨 sourceId 沿用
- 证据：`DatabaseDetailView.vue:59,86–115` 草稿仅初始化一次，watch sourceId 调 initialize 但不清草稿；131–170连接/诊断没有 sequence、目标核对、取消；173–180错误诊断用当前 source重建。
- 风险：同一Vue组件A→B时，A凭据留在B表单，用户可能误提交到B；A迟来的诊断覆盖B。
- 建议：来源切换清空敏感草稿和错误，捕获目标，绑定 route request generation/AbortSignal。可复用数据库表页模式。
- 验收：A输入的用户名/密码/库名在B均清空；旧诊断永不显示在B；卸载/退出后的结果不更新提示。

### F04 / P1：容器详情可能错配标题和内容
- 证据：`DockerView.vue:353–368` 先设置 logContainerName，再 Promise.allSettled，返回不校验id/序号；快速A→B两次调用共写 logs/details/loading。
- 建议：详情读取独立 composable，abort+request key；关闭时失效。
- 验收：A慢、B快，标题、详情和日志都归B；A返回不能清除B的loading或写B错误。

### F05 / P2：日志“加载更多”在切换条件后可能永久 busy
- 证据：`LogsView.vue:115–138` loadMore置loadingMore=true，finally只在sequence相等时清理；`load():62–80`改变sequence却不重置loadingMore；117据busy拦截下一次加载。
- 情景：加载下一页时换来源/时间；旧请求取消且sequence已变，loadingMore不归零。
- 验收：上述操作后新筛选仍可加载下一页；取消、错误均清对应busy；慢历史响应不得覆盖已经到达的实时条目（95–99 与157–160并行写entries也需测试）。

### F06 / P2：移动导航关闭后仍可能键盘可达；打开后缺少焦点隔离
- 证据：`AppShell.vue:70–85`只管body滚动与Escape返回菜单按钮；100侧栏始终挂载；252依靠translateX移出屏幕；未见inert、visibility隐藏、focus trap、打开时focus、背景inert。
- 影响：Tab可能进入屏外链接；菜单打开时Tab进入遮罩后背景。当前Escape与按钮aria属性是可保留的基础。
- 验收：390/768宽关闭菜单Tab不进入屏外项；打开焦点进入菜单，循环/退出策略符合选择的dialog或disclosure模式；Escape关闭并恢复按钮；路由选择后聚焦main。

### F07 / P2：数据库深链首次加载时“资源不存在”过早出现
- 证据：`DatabaseDetailView.vue:93`先异步发现，顶层template按source存在与否分支，361直接“不存在”；`DatabaseTableView.vue:156–170,634`同类。
- 建议：区分discovering、not-found、requires-connection、error，只有已完成有效发现后判定不存在。
- 验收：清store打开深链，延迟发现时只见loading；404/连接需要凭据/服务失败文案独立；不可先显示误导性不存在。

### F08 / P2：总览收藏区把加载中/失败呈现为“还没有收藏”
- 证据：`OverviewView.vue:76–77`异步refresh；114–118仅凭favorite列表空呈现“还没有”；`stores/sites.ts:47–66`明确提供loading/error却未用于该分支。
- 建议：首次骨架、错误重试、成功真空、缓存更新四态；避免失败时鼓励用户重新收藏。
- 验收：sites 500时显示失败；首次慢请求没有短暂“还没有”；旧收藏保留并标记刷新失败。

### F09 / P2：跨模块取消、错误协议、部分成功反馈未完全收敛
- 证据：system store `216–257`有session、dedupe和逐资源结果；Infrastructure `180–207`有latest gate但卸载352只注销refresh；Users `47–58`无signal/sequence且all-or-nothing；control `406–435`和database API `165–189`仅将成功JSON断言为T，与systemTransport `19–39`运行时validate不同。
- 补充：`stores/sites.ts:70–75,102–118`忽略refresh返回false，而remove `96–99`抛SiteSyncError；`ServicesView.vue:257–263`保存可显示成功但列表仍旧。应分别表达“写成功，列表同步失败”。
- 验收：同一资源latest wins；登出不写旧状态；单个辅助接口失败保留主列表；损坏JSON shape进入稳定错误面板；每个写操作区分提交失败、成功未同步、已同步。

### F10 / P2：数据不可用与“仍在加载”的无障碍语义需分开
- 证据：`RealtimeTrendChart.vue:224` aria-busy=!enoughData，空时间范围可永久busy；231–234提供最新值很好，但不能替代趋势的可读说明。`MonitoringView.vue:129–131`总是清空samples，因此298“loading && samples.length”的保留旧图更新态通常不可达。
- 建议：由父层传loading；真空aria-busy=false；同范围刷新保留旧图，异范围清空；增加时间窗口/极值等可读摘要。
- 验收：加载结束零样本不busy；失败不显示无限等待；刷新错误可继续查看上次数据并知道时间。

### F11 / P3：导航层级与tab语义分裂
- 证据：`AppShell.vue:195–202`仅exact-active着色，数据库详情/表路由与列表是平级独立route，数据库主入口未显式按prefix选中；`SegmentedTabs.vue:70–83`用tablist/tab，panelId可缺；`SectionTabs.vue:37–38`用group/aria-pressed；`ServicesView.vue:357`状态筛选使用tab但无对应tabpanel。
- 建议：导航按模块id声明活跃态；页面分区用tablist/tabpanel，筛选用radio/group按语义选择，共享roving键盘实现。
- 验收：数据库三级页面侧栏持续标注数据库；读屏可说出当前页/筛选；每个tab有对应panel或改用合适语义。

### F12 / P3：登录首启文案和账号产品模型不一致
- 证据：`LoginView.vue:24`“唯一的Root管理账号”；`UsersView.vue:179`管理多个等权Root账号。初始化password仍用current-password（95），确认字段用new-password（107）。
- 建议：“创建首个Root管理账号”，初始化两密码均new-password；密码规则与服务端真实policy匹配。
- 验收：首启/正常登录文案和autocomplete分支一致，密码管理器不会把新账号流程当旧密码登录。

## 设计基础：应保留什么，需验证什么

- 颜色：`styles/tokens.css:4–125`已区分画布、surface、主色、状态、资源对象、图表、终端色；190–226映射Element Plus。保留语义区分，不要给每页重新定义颜色。
- 尺寸：133–156有radius和4px间距；175–188有max-width、padding、44px触控、232/68侧栏；229–235紧凑模式变量。
- 字体：`main.ts:1`预载JetBrains Mono；tokens158–164字体栈；`stores/system.ts:403–429`按偏好设置字体并动态import。需要验证Noto CSS真实family与`'Noto Sans SC'`是否一致、字体加载失败fallback、Windows/macOS/Linux最终字形、15px与最大字体时截断。不可凭package里列了字体就声称已显示该字体。
- 图标：本次src搜索以`@lucide/vue`为主，未见`@element-plus/icons-vue`直接引用（package仍列依赖）。不要仅因依赖存在就认定混用了两套图标。实体图标和状态色有可复用token。
- 动效：`styles/motion.css:24–47`尊重reduced motion，spinner也由全局规则减弱；`RealtimeTrendChart.vue:35`初次读取matchMedia，运行时更改系统偏好未响应，需测试；终端cursorBlink（TerminalView130–136）不受CSS动画规则控制。
- 可访问性：base64–67全局focus-visible，83–93 sr-only，AppShell90 skip link，router90–95路由后main聚焦；保留这些。`router`的单次rAF是否早于lazy view挂载，应在冷缓存导航验证。
- 维护风险：ServicesView553/558/559之后又有715+新布局断点；TerminalView416–421连续追加长CSS，后规则覆盖高度。已有token但不少固定色、px和紧凑度例外。应按模块归并CSS，保持结果后再改视觉，避免一次全局改动影响所有业务。
- 窄屏候选风险：AppShell导航10项+底部身份的100dvh且侧栏未设内容滚动（184–203）；Terminal最小485px（418）叠加header/toolbar，移动软键盘时需真实可用性验证；Users600px以下隐藏time（272），时间信息可能无法再获得；base15 overflow-x:hidden可能掩盖裁切，截图和控件bounding box验证不能只测body scrollWidth。
- 小字候选：Overview metric detail `.63rem`（172）、trend empty `.64rem`（RealtimeTrendChart245）、终端能力chip `.68rem`（Terminal419）；按默认15px约9.5–10.2px。是CSS事实，不直接等同WCAG失败，需展示优先级和可读性判断。

## 已有复用组件与建议独立模块

### 直接复用，不需要重建
- 页面壳/标题：AppShell、WorkspaceHeader、SectionHeader。
- 行为控件：ActionButton、ListIconButton、NcpSelect、SegmentedTabs、SectionTabs、CompactSegmentedFilter、ListPageSizeControl、ResourcePagination。
- 状态：ResourceState（loading/empty/error，role与retry，22–34）、DatabaseErrorPanel、StatusPill、ConfirmDangerDialog。
- 数据展示：GroupedDirectory、MetricCard、RealtimeTrendChart、SignalChart、DateTimeRangeControl。
- 编辑器：PlainCodeEditor、SqlEditor、DatabaseCellEditor。
- 业务边界：Infrastructure各Panel；DockerContainerPanel/ImagePanel/HubBrowser、CreateContainerDrawer、ProjectDetailDrawer、ContainerLogDrawer、ComposeEditorDrawer。
- 生命周期能力：sessionLifecycle（generation+signal），requestSequence（latest gate），manualRefresh registry，useListPreference；数据库表页是route scoped取消的实例。

### 可独立排期且边界清晰
1. `ShellNavigation`：壳、移动导航、模块活跃、面包屑、路由focus；不改业务API。验收F06/F11及冷加载聚焦。
2. `AsyncResourceState`：统一加载/空/错误/旧数据/刷新契约，ResourceState扩展与API错误映射；先接总览、数据库深链、监控。验收F07/F08/F10。
3. `DockerDetailSession`：容器抽屉请求隔离；Project/View仅传目标；验收F04。
4. `ComposeWorkspaceSession`：项目/文件/revision草稿、dirty、校验/确认冻结、Job观察；验收F01/F02，不能与纯视觉重排混做。
5. `DatabaseConnectionSession`：详情页草稿、诊断、连接状态、敏感清理；与database table的请求策略对齐；验收F03。
6. `LogQuerySession`：history/follow/cursor/query key/busy收敛；验收F05，补500条裁剪和去重行为。
7. `DesignFoundationCleanup`：token/排版/断点/CSS分段归并；只在有截图后安排，对照1440/1280/768/390及200%字体。
8. `OperationOutcomeFeedback`：用户、站点、DNS、Docker动作的已提交/执行中/失败/部分失败/待同步一致反馈；不自动重发有副作用的请求。

## 后续本轮截图与交互验收矩阵

主线程补截图时应记录URL、viewport、状态、真实数据/fixture、时间，每张实际打开检查后编号。不得以历史快照或帮助文档代替本轮截图。

1. 首启/登录/离线各1张，截图避开密码和敏感字段。
2. 十个主入口1440×900初始稳态；数据库详情/表再各1张。
3. 390×844对全部入口的关键区域、主菜单开/关、长名称、宽表做覆盖；768×1024检查导航断点；1280×800检查紧凑桌面。
4. Docker子视图、Hub列表/标签、创建预览、详情、Compose编辑/历史/确认；只打开与取消，不执行真实管理动作。
5. 系统各tab，DNS不可用与预览前编辑，代理检查错误；不确认应用真实DNS。
6. 各模块首次慢请求、成功空、筛选空、500、部分失败、缓存刷新失败；测量layout shift与加载反馈。
7. Tab/Shift+Tab/方向键/Home/End/Escape；抽屉关闭后焦点返回；只读屏也能知道对象、状态、下一步。
8. F01–F05用受控fixture/mock延迟响应验证；不要用真实NAS写入复现。
9. reduced-motion、最大字体/200% zoom、字体禁用/加载失败、窄高屏和软键盘。
10. 真实API smoke限授权只读；mock截图只能证明fixture下前端，不能证明后端已部署/真实资源可管理。

## 测试资产与证据限度

- `e2e/pages.spec.ts:6–58`已有12路由×4viewport、console/pageerror和body/main宽度检查；未涵盖登录与多数打开的弹层，也不等于控件没有裁切。
- `e2e/accessibility.spec.ts:25–42`仅1440宽、mock、critical/serious axe筛选；不覆盖中低影响、完整键盘路径、移动菜单、读屏和焦点行为。
- `e2e/app.spec.ts`总览快照、`system.spec.ts`多tab及竞态/失败/DNS取消、`docker.spec.ts`生命周期/Hub/创建、`database.spec.ts`危险SQL取消/发现失败、`logs.spec.ts`历史分页可作为扩展入口。
- src已有auth/system/sites/database store、requestSequence、network、monitoring series、终端策略等unit tests。此轮只读了源码与测试定义，未运行，不能报告通过率。

最小交付门槛建议：解决F01–F06；全主路由/关键抽屉的本轮截图；所有核心状态有真实可恢复路径；权限内完成既定类型检查与生产构建；真实部署健康状态由负责执行环境的任务另行确认。前端静态审查不能代替部署验证。
