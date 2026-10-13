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


## M18：站点写入与列表同步分态（实施中）

F09现状复核仍成立：站点save/create/restore/icon写入完成后忽略refresh=false，界面可能以旧列表宣布完整成功；remove已有SiteSyncError可复用。本片只收敛站点编辑与对应刷新反馈，不扩展协议或真实服务。

- 写入失败仍保持失败与草稿；写入已成功但读取列表失败明确提示部分成功，提供只读重新同步，不自动重放create/update/delete/upload。
- 创建后的真实projectId必须随部分成功结果保留，避免重试“添加”创建重复站点；不能把后置refresh错误放入图标失败rollback的catch中。
- 操作捕获会话、编辑目标及提交版本；关闭/切对象/重新登录后迟到响应不覆盖当前草稿、提示或busy。
- 真正读失败与旧读取被新读取替代需要区分，不用旧请求的finally清掉新请求状态；全局目录与忽略列表仍按原契约同步。
- 纯mock回归包括写成功读失败、只读重试次数、创建ID保留、迟到操作、会话重置及图标两阶段边界；静态构建与后续浏览器fixture分别验收。

已有create图标失败补偿删除路径会吞掉补偿失败，可能留下站点；需明确记录部分结果，不能借本片新增自动删除或声称跨HTTP事务原子。实现是否包含最小结果分类由精确变更审查确定。

M18-A实现与独立审查完成：复用SiteSyncError并附已写入ID，后置refresh置于图标补偿catch之外；新成功同步可覆盖被替代旧读取，避免误警告。编辑器保留新草稿、只读重查、采用已创建ID进行后续更新，提交/校验捕获快照并隔离会话与编辑代次；收藏按资源独立等待，保存期间不接受图标变更。720项单元、lint/typecheck/构建通过；五项浏览器fixture及实际画面待CI。图标失败且补偿删除失败的结果不确定性仍属于M18-B，未在本片掩盖或宣称已解决。


### M18-B前置：站点读取契约与结果不确定性

已确认requestSites会把缺sites或sites:null等坏成功响应归一为空表，requestIgnoredSites没有运行时校验。先交付只读响应校验：顶层集合与身份无效则整体稳定报错，不过滤坏行冒充完整；保留原有缺discovery、nullish说明/图标/分类/入口与ports的旧服务端默认兼容。忽略资料的name空串是合法占位，不能套用创建输入的minLength1规则；OpenAPI应独立描述包含projectId及实际metadata的SiteProfileResponse。

创建/更新的2xx坏响应可能已写入，故本片先不接入生产写响应校验，避免让界面把新增协议错误当作可以直接重提的普通写失败。写侧身份校验需和后续“结果未确认”消费逻辑共同落地。

图标失败且一次补偿DELETE也失败时，应保留真实createdId，不自动补偿第二次或重放创建；两份已验证列表只能支持“本次核对未发现该手工记录”。两GET非原子，目录collectedAt不是资料版本；不能证明补偿成功、未来不会出现或图标文件已清理。discovery=complete更不能证明整个NAS资源不存在。

读取前置实现完成：新增站点专用解析器，GET sites/ignored接入整份校验，Site.state按后端契约保持string；create/update仅有可复用解析器与纯测试，生产调用未接入。独立SiteProfileResponse描述实际Go类型，忽略空名、projectId和元数据同步，输入要求不变。789项全量单元、lint/typecheck/构建及OpenAPI全套通过，独立审查无阻断；后端仅验证真实Go值的JSON及schema，不称endpoint已运行。


### M18-B：未知写结果只读核对（16:33 UTC，本地实现就绪）

以下接续前置读取切片，不改写上面的历史状态。生产POST/PUT现接入资料响应校验：成功HTTP状态的坏JSON、空体或不合法资料/身份进入`SITE_WRITE_UNKNOWN`；创建必须得到可信手工站点ID，更新必须匹配原目标ID。未知创建不从名称、列表差异或坏响应猜ID，不继续图标上传或删除；未知更新保留请求的原目标。

- 新建后图标失败仍只执行既有一次补偿DELETE；DELETE也失败时保留已确认的创建ID并报告补偿结果未确认，不吞错、不再次删除或重放创建。补偿成功后重新检查原会话才启动列表读取，关闭了等待DELETE期间会话失效后污染新会话loading的竞态。
- 编辑器保留草稿，当前未知操作暂停提交与图标上传，只提供关闭和GET-only核对。核对分别陈述目录存在、已忽略、两表同时存在、两表均未发现、读取失败或无可信ID；所有观察都保持当前编辑器的未知结果限制，不自动恢复提交。关闭后重新打开是新的编辑操作，限制不是跨页面或持久化锁。
- 收藏按资源ID限制本页重复提交，其他站点仍可操作；只读刷新不解除未知收藏限制。会话、编辑代次和卸载隔离继续阻止迟到结果覆盖新页面。
- 两GET不是原子快照，未发现不证明回滚成功、未来不会出现或图标文件已清理；即使DELETE已确认，也不能阻止尚未完成的服务端图标请求迟到upsert。未知POST缺少可信ID时仍无法定位本次记录。普通POST/PUT网络失败没有在本片全面重分类为未知结果，亦未解决所有跨模块F09。

实施者报告59文件821项单元、lint、typecheck、生产构建及包预算通过；独立审查在最终竞态修复前通过820项，最终修复复审无阻断。主线程另独立复跑完整59文件821项单元通过（20.61s），diff检查通过；本记录已核对实际API/store/view及回归diff。新增10项M18-B浏览器fixture已编写，静态检查与用例发现通过，尚未实际执行或推送，不把本地实现就绪写成浏览器验收完成。

M18-A与读取前置的后代`b0c21a9`，[CI 38430440344](https://github.com/Kkwans/nas-control-plane/actions/runs/38430440344)已通过789项单元、211项浏览器和Go/Compose；四张实际PNG与对应JSON已复核通过，见[站点恢复视觉报告](../qa/NCP-2026-10-13-site-sync-visual.md)；关闭32×22px、重试116×32px不等于44px触控目标，200%根字号也不等于浏览器整页zoom。此前`b2c64c`的[CI 38429724168](https://github.com/Kkwans/nas-control-plane/actions/runs/38429724168)为789项单元、209项浏览器通过/同两项对比度失败，后代绿色闭环保留此前失败历史。该CI不包含本节尚未推送的M18-B，也不证明NAS或部署已验收。

## M26：创建容器草稿与提交归属（实施中）

源码复核基线 fbb203a。`CreateContainerDrawer.vue` 的 POST 成功回调无条件关闭抽屉、清除提交草稿；用户提交 A 后关闭并打开 B，A 的迟到成功会关闭 B。资源清单读取也可能把用户新输入的专用网络名换成旧清单首项。页面/认证失效后仍可能通知并触发父级资源刷新。原“取消不会创建任何 Docker 对象”文案不能描述已经发出的 POST。

首片边界：为每次打开/镜像身份/会话绑定草稿与读取归属；冻结预览输入并一次性消费确认，关闭或身份变化使旧预览失效；同一组件的提交锁保持到原 POST 结束，关闭不代表远端取消。旧完成不能关闭或清除新草稿，失效页面/会话不得通知或启动刷新。资源与路径选择读取取消/代次隔离，不覆盖用户已编辑网络，也不让删除挂载行的迟到目录结果落到另一行。保留原参数验证与实际创建能力，不新增后台任务或自动重放。

验证使用 Vue/jsdom 与 deferred Promise：POST A→关闭→B草稿→A成功/失败；同开窗镜像变化、退出/卸载、资源A慢B快、读取中编辑网络、路径选择关闭/行删除。后续实际浏览器验证普通X/Escape及新草稿完整，使用内存API，不能创建真实容器。坏成功响应/普通网络错误导致的创建结果不确定性留独立设计，不凭名称猜测已创建对象，不把只读复核当成自动重试。

### M26 首片实现与本地验证

已实现开窗/镜像/会话/草稿版本归属，预览确认一次性消费，原 POST 完成前保持提交锁。旧成功仅在同页同会话发出 created 做只读对账，不关闭新草稿或显示旧提示；离场和会话失效不发事件。网络读取不再自动改写网络名，目录读取使用对象身份与取消信号，关闭或删除挂载行使旧返回失效。两个 API 读取包装追加可选 signal，保留原 fetcher 参数位置。

独立审查另外复现父级创建后对账的二段读取漏洞：等待 inventory 后离场仍会 warning 并读取 images。已在父级等待后验证挂载状态和原会话，再进行后续提示/读取；新增四项回归，独立复验闭环。核心组件15项、读取转发4项、父级面板13项共32项聚焦通过；主任务全量77文件/1360项单元、类型检查、lint、生产构建与包预算通过。六项实际浏览器fixture完成静态检查，X/Escape、新草稿及目录选择的真实交互与截图仍待CI。没有运行真实创建容器或访问NAS。

## M27：创建成功响应损坏时的未确认结果（实施中）

M26之后核对发现：浏览器收到2xx但JSON/字段无效时，现有抽屉把它当普通错误并恢复提交按钮；此时服务器可能已创建容器，重复提交有风险。当前完整结果校验还接受空ID/image/state及与提交意图矛盾的started/runContainer。后端启动或inspect失败会尝试补偿删除，错误却不携带可信创建ID，因此不把错误码或坏响应中的看似合法ID当成创建凭证。

首片只专门处理成功HTTP响应的无效完整结果：合法非空ID/image/state、created=true且启动字段与请求意图一致才走现有成功流；坏JSON/空体/无效字段变为专用“结果未确认”错误，不携带原始响应或部分ID。普通fetch拒绝及非2xx分类保持现有行为，不能宣称覆盖全部创建不确定性。

UI保留当前页/会话内的一份最小尝试摘要（镜像、名称、是否启动、时间），不额外保存环境变量等完整敏感配置。未知状态独立于当前草稿编辑版本，A关闭后B仍可编辑但不能再提交；不关闭B、不发created或成功提示。重复只读GET、空列表、同名同镜像记录及读取失败都不能自动确认结果或解锁重放。不推断身份，不自动POST/start/delete，不增加后台轮询或持久凭证。退出/卸载清理并明确本页通知不是跨页面恢复记录。

验收：合法仅创建/创建启动、201坏JSON与204空体、空字段/矛盾意图；未知阻止所有提交入口与旧确认，关闭重开不清除警告；只读检查A慢B快及失效会话不回写；同名/空结果不解除未知。全部采用纯fake及内存浏览器fixture，禁止真实创建或状态更改。

### M27 首片实现与审查（20:32 UTC）

专用成功响应解析已按提交前捕获的启动意图核验完整结果；state 仅接受既有协议的 running/stopped，启动成功后进程退出仍可为 stopped。专用错误不保存坏响应、部分 ID 或敏感配置。OpenAPI 同步非空 ID/image 与 created=true 限制，请求意图关联由客户端校验。

未知提示保留不可变最小尝试摘要，所有提交入口锁定；关闭重开和新草稿不清除该尝试。只读核对不发 created、不推断同名身份、不自动解锁。独立审查发现共享 inventory 解码仅检查数组，新通知渲染 null 行可能崩溃；本片已在核对路径逐行验证并复制五个展示字段，坏行转为通用读取失败且保持锁，新增十四项回归并复审关闭。

独立 API/抽屉 78 项通过；主任务从 17afb264 加本片精确九文件快照验证 78 文件 / 1424 项单元、全量 lint、类型检查、生产构建与包预算，并通过 OpenAPI 合同/解析验证。八项内存浏览器用例完成静态检查与发现，390/1440实际像素、交互与完整axe仍待CI。普通网络拒绝及非2xx不确定性仍未纳入本片，不宣称已全面解决创建重复风险。
