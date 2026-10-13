# M15-C 候选：终端标签页与多会话边界

日期：2026-10-13。审查基线：fe92e4506c4b23119c63daaf89b236fcd22f2ee7。状态：单终端生命周期 P 片已实现并完成源码复审，28 项纯 fake 单元回归、前端类型检查及定向 ESLint 通过；6 项浏览器 fixture 仅完成静态检查，运行验证待隔离 CI。主任务独立全量61文件964项单元、lint、类型检查、生产构建及540KiB预算通过；浏览器CI待验。多会话 M 片仍暂缓；未连接真实终端，未执行终端命令或真实 WebSocket/PTY/Docker/NAS 验证。

## 结论与决策门

**现有生产后端不支持两个并发终端，不能把纯前端标签页作为“多会话已完成”交付。** `internal/agentsocket/server.go` 默认创建 `NewPOCManager()`，后者在 `internal/terminal/poc.go` 使用 `defaultMaxSessions=1`。`Manager` 虽接受可注入容量，但未发现生产容量配置入口。此上限为该 Agent Manager 的全局主机/容器合计，不是每标签页、每浏览器或每用户。

主任务已决定：暂缓标签页，不交付单连接标签页替代品，也不扩充后端容量。本次已实施独立的 **P 片：单终端连接生命周期修复**，验证进度见文末。以下多会话设计仅供未来 **M 片**使用，必须另行决定 Agent 全局容量配置、部署预算与协议需求，不能通过 P 片顺手把默认容量从 1 改成 4。

P 片不改变 Root 身份、Shell、执行参数、RPC/WebSocket 协议或宿主权限。未来 M 片保留权限与执行边界，容量配置及是否需要新增观测协议须先决定。均不增加任意命令接口、启动命令、广播输入、会话恢复、后台常驻、跨窗口同步或输出持久化。

## 基线审查事实与持续风险

以下前端缺陷描述对应上述审查基线，P 片已修复的范围见文末；后端容量、远端退出与浏览器内存边界未改变。

- `frontend/src/views/TerminalView.vue` 基线版本以单实例闭包持有 socket/xterm/FitAddon/监听器；10 秒握手等待，5000 行 scrollback，80ms resize debounce。目标选择在 connected 时禁用，但 connecting 时仍能改，可能让标签描述偏离已发请求。没有连接中取消按钮。
- 旧回调直接读写共享变量；`await nextTick()` 后没有生命周期复核。连接取消/重连/卸载后，晚到 started、close、error、resize 或粘贴焦点回调可能修改新连接。不得把现有组件原样复制四份后宣布安全。
- `frontend/src/session/sessionLifecycle.ts` 提供 generation/signal，基线终端尚未接入；当时仅 `onBeforeUnmount(close)`。`App.vue` 登出会同步 invalidateSession，路由未使用 KeepAlive。
- `internal/terminal/manager.go` 正确计入 opening + live + closing；失败但返回 session 的 Start 也由 Manager 唯一 Close，Close 返回后才释放 closing 名额。前端不能据本地删除推断服务端有空位。
- `internal/httpapi/terminal.go` 每个 WebSocket 对应独占 Agent stream；默认 30 分钟会话超时。浏览器 read pump 在收到 Agent started 后才启动：启动尚未完成时本地取消不构成立即服务端取消/回收证据。
- `/ws/terminal` 经 requireAuthentication + requireLiveSession；认证撤销取消租约 context，继而关闭传输并走 Agent 清理。认证流还有默认 256 的全局上限，与终端容量不同，标签数不能代表可用配额。
- Agent 16KiB 输出块和串行发送不等于浏览器内存小额有界。已检查安装依赖 `frontend/node_modules/@xterm/xterm/src/common/input/WriteBuffer.ts:20,103–107,127–129`：异步 write 有 50,000,000 的 pendingData 水位，超过才抛错，检查发生在当前帧加入前；不是“无界队列”，也不是精确 50MB 总内存硬限。基线 `TerminalView.vue:227` 没捕获该异常；P 片现已捕获并终止当前 owner，不新增输出预算策略。5000 行 scrollback 与此独立，浏览器网络接收队列不由该水位控制。
- `TERMINAL_SESSION_LIMIT_REACHED` 经 Agent/Server 原样 code 到浏览器，基线前端缺少专门文案，P 片已补充容量不足与手动重试提示；升级前认证/流上限失败通常只能表现为 WebSocket error，不可猜测具体原因。
- `closed` 帧可能在 Agent deferred cleanup 前发出；显式 close 还可能直接结束代理。帧或浏览器 onclose 均不证明远端进程、全部后代已退出，也不证明命令副作用撤销。

## 未来 M 片最小 UI 与所有权（未批准）

1. 页面初始一个未连接标签，最多四个；新增只创建空白目标选择，不创建 xterm/连接，不继承或执行任何输入。最后一页关闭后保留一个新的未连接页。标签 ID 单调唯一，与服务端 sessionId 分开；目标标题使用连接前快照，不硬编码实际 NAS 名称。
2. 每标签有独立 target/containerId、连接快照、状态、能力、输出对象、粘贴确认和 attempt token。连接中、已连接、关闭中锁定该次目标；一份纯生命周期控制器拥有该次 socket、订阅与 timers。Vue 组件只负责展示与事件，不能把非响应式 socket/xterm 交给深响应代理。
3. 只有明确“连接终端”创建连接；本页 opening/live/closing 合计达到批准的并发上限时禁用新连接并说明原因。切换、新增、关闭相邻标签、页面恢复均不得连接、重连、发送字符、复制会话或运行命令。跨窗口竞争仍由服务端裁决。
4. 隐藏标签保持其现有连接和有界 xterm 状态，不卸载重建、不暂停后续接收而堆积原始输出。隐藏面板不可聚焦、不可访问输入；切换时取消原标签未确认的粘贴，绝不把粘贴带到另一连接。
5. 连接中显示“取消连接”；已连接显示“断开连接”。关闭活动标签需明确说明连接将断开、远端任务是否结束无法保证；确认后标记 closing，禁止输入并清空粘贴，尽力发送既有 close 控制帧，然后关闭 socket。所有 send/close 异常仍须进入 finally 清理。
6. 本地 closing 计入本页连接限制，直到该 socket onclose；删除活动标签保留控制器的退役记录直到收尾，不能借删页立即开新连接。若浏览器一直不报告 close，保持准确的关闭中提示，不假定超时后后端已释放。路由离开时只能尽力关闭，不能阻止浏览器永久退出。
7. 使用“已请求断开/本地连接已关闭，服务端资源回收可能仍在进行”；不使用“任务已取消/进程已结束”。连接容量拒绝保留原标签与安全错误文案，手动重试；不轮询或自动重放输入。不能从本地 onclose 消除服务器仍在 opening/closing 的事实。

## 生命周期不变量（P 片先落实单终端，标签项留给 M 片）

- 任意异步边界之后复核 page alive、认证 signal/generation、tab ID、attempt ID 与准确 socket identity；失效结果不得写状态、创建资源、发送输入或抢焦点。旧 onclose 不得关闭新 socket。
- 先使 attempt 失效、撤销输入，再幂等清理；每份 xterm/observer/DOM paste 监听/window resize/visualViewport resize/timer/subscription 恰好释放一次。清理中的 transport onclose 只能完成该退役控制器，不能复活标签。
- `started` 仅接受一次且仅在当前 connecting；关闭/错误/超时之后的 started 与输出丢弃。输出仅接纳当前已 started 的连接。WebSocket 构造、xterm open/fit、send 和 dispose 异常均有终止路径。
- 监听 captureSession().signal；invalidateSession/beginSession、页面卸载、pagehide 均立即拒绝输入、关闭全部本地连接、销毁输出/粘贴与敏感快照。BFCache 返回保持未连接，不自动恢复。visibilitychange 到 hidden 不等同 pagehide，不能仅因切到别的浏览器页而断开。
- 页外不保留全局 terminal store、连接或输出。后端会话撤销依然是权限边界，前端防护只是尽早停止交互。

## 未来 M 片的输出、尺寸与可访问性（未批准）

- 每个终端保留既有 5000 行 scrollback；待 xterm write 回调确认消费的原始字节总量最多 256KiB，每帧先检查长度再分配/入队，计数包含已交给 xterm 但尚未回调的数据。超限终止该连接并提示“输出处理过载，连接已断开，后续输出未保留”，不静默截断后继续解析 ANSI，不积累额外历史字符串。
- 当前 Agent 每帧至多 16KiB；前端拒绝异常大帧。上限是应用持有的待处理字节，不宣称限制浏览器网络缓存、xterm 全部对象内存或远端进程输出。输入同样检查 32KiB 既有单帧上限与 socket.bufferedAmount，超限不拆分重放命令。
- 仅 selected 且布局非零时 fit；隐藏标签保留最后有效尺寸。重新选中后 nextTick 复核身份再 fit；rows 限 1–200、cols 限 1–400，与服务器一致。能力明确 false 时不发 resize，resize 回调也需选中/连接身份复核。
- 服务端 started 不自动从其他标签或工具按钮抢焦点。键盘切换标签后焦点留在标签，可通过 Tab 进入面板；只有显式连接/点击输入且焦点意图仍有效时才聚焦 xterm。
- 使用 tablist/tab/tabpanel、唯一 aria-controls/labelledby、aria-selected、roving tabindex，箭头/Home/End 管理标签焦点与选择。关闭按钮独立于 tab 按钮，带完整目标名；关闭后聚焦相邻标签，新建后聚焦新标签。快捷键只在标签条生效，不能夺取 PTY 常用键。
- 隐藏面板从键盘与辅助技术树排除；仅选中页状态以 polite 通知，后台状态放在标签可读名称，不把输出刷入 live region。每页粘贴标题 ID 唯一。
- reduced-motion 改变更新所有现存 xterm 的 cursorBlink，不重连或重建；新增实例采用当前偏好；标签/面板动画在减少动效时禁用。窄屏标签可横向滚动，操作按钮可见且有焦点样式。

## 未来 M 片验收矩阵（另行批准后执行）

- 纯 fake socket/xterm：四页上限、第五页拒绝、新增/切换零连接零输入；批准的并发上限计入所有 opening/live/closing；后台连接保留且不 fit/focus；清空仅影响当前页显示。
- 竞态：connect → nextTick 前关闭/删除/卸载/认证撤销；旧 started/error/close/输出/write 回调落到重连之后；相邻页同时事件；构造、open、fit、send、close 抛错；重复 close/abort、握手超时后 late started；退役 socket 永不 onclose。
- 目标/粘贴：连接中锁定目标；容器库存变更不改已连接快照；不同页独立能力；多行确认后切页/断开/重连/撤销不发送；旧确认与旧焦点 nextTick 无效；不自动执行输入。
- 输出：256KiB 精确边界、超一字节、回调延迟/乱序/关闭后回调、异常大帧、隐藏页持续输出、5000 行设置；超限仅关闭该 owner，不插入任意终端控制输入。
- 收尾：route unmount、pagehide/BFCache、认证失效/beginSession 同步禁输入；所有资源与监听恰好清理一次；浏览器 onclose 后服务器仍拒绝容量时准确提示；不开重试风暴。
- 交互：tab ARIA 关联、键盘方向/Home/End/关闭焦点、paste dialog 唯一 ID、后台不抢焦点、不广播输出；动态 reduced-motion；窄屏溢出、长容器名、错误/关闭中状态。
- 现有后端纯 fake 证据继续覆盖 opening/live/closing 全局限额、晚到 Start 唯一 owner、撤销阻断输入与输出。M 片另外验证容量接线和四路不同 host/container starter 并发隔离、关闭一个不影响其他；不以多标签 mock 成功代替生产容量验收。
- M 片仍只有方案，未实施或运行其多会话验收。P 片本地仅运行纯 fake/静态检查；任何真实 PTY、terminal 命令、WebSocket、listener、Docker/NAS 连接需依照主任务当次授权单独安排。生产构建与既有隔离 CI 的通过不等同 NAS 实机验收。

## 建议阶段与改动范围

1. 主任务已批准并实施下述 P 片；源码复审与定向 fake 回归已通过，浏览器运行及最终集成验收仍待主任务验证。
2. P 片只改 TerminalView、必要的局部 attempt owner 与 mock 测试；不做标签、队列容量策略、后端容量或协议变化。
3. 未来 M 片须明确 Agent 全局容量、部署资源预算、并发 Starter 验证；决定是否新增容量/关闭确认协议。仅沿用现有协议也能做有限多路连接，但无法报告准确可用名额、确认启动中取消已回收、确认全部进程退出。若产品要求这些保证，需独立协议方案与 OpenAPI 同步；不能仅修改 UI 文案伪造保证。

## P 片：单终端连接与认证生命周期（已实施，待最终集成验收）

### 基线精确证据及需保留的保护

以下缺陷与行号均为上述基线的 `frontend/src/views/TerminalView.vue`，用于解释修复原因，并非当前实现状态：

- **P0 旧事件可操作新连接**：181–240 的 message/error/close 回调捕获共享 socket/terminal/state，没有 attempt identity；旧 error/无效 JSON 可在 198/216 关闭新 socket，旧 onclose 在 233–235 禁止新会话输入并取消它的握手定时器。176–179 的 timeout 同样只有全局 state 检查；242–247 的旧 xterm input/resize 可写新 socket。
- **P0 认证失效未立即封闭输入**：296–301 只检查 terminalReady/OPEN，没有 captureSession signal/generation；369 仅靠组件卸载 close。后端有 live-session 撤销，但前端不能把 DOM 下一轮卸载当作同步输入隔离。
- **P1 等待边界与初始化失败**：120–135 在 nextTick 后不复核 attempt/session/disposed。卸载后 element 为空会进入 failTerminal，至少还能修改失效页面状态；取消但 DOM 仍在时可能继续创建终端。136–174 初始化没有 catch/finally，构造/open/fit/WebSocket 抛错会留半初始化资源。
- **P1 用户无法取消 connecting**：386–387 只有加载中的连接按钮，断开仅 connected 可见；379–382 目标在 connecting 仍可改变，已发 query 与显示状态可不一致。
- **P1 关闭异常中断清理**：357 的 close 控制帧 send 和 358 的 socket.close 没有保护，抛错会跳过 terminal.dispose/state 收尾。232–240 自然关闭不清 pendingPaste；旧确认可留到关闭画面（现有 send guard 通常阻止发送，但不应保留过期文本）。
- 已有保护必须保留：121 的重复连接拒绝；176 的 10 秒等待；260 的粘贴就绪检查；296 的输入就绪/OPEN 检查；315–320 fail 时禁输入并清粘贴；339–366 正常 close 清定时器/观察器/DOM/window 监听并 dispose；114–116 动态减少动效。
- `TerminalView.test.ts:40–98` 只有两种 reduced-motion 初值参数化用例，覆盖偏好变化不重建、显式断开/重连/卸载各 dispose 一次。fake readyState 一直为 0，onData/onResize 没保存回调；未覆盖 OPEN send/close、认证、旧事件、pending nextTick、取消按钮或异常清理。不能把这两项通过当成上述保护已有回归验证。

### 已实施的最小修复契约

- 每次 connect 在等待之前捕获不可变 target/container 与 page/auth/attempt 身份；nextTick 后复核，资源始终由该 attempt 独占。先创建局部资源，再注册闭包；闭包只操作准确 owner，不查可能已替换的共享 socket。
- 连接中显示“取消连接”；目标选择 connecting/connected/closing 禁用。cancel 先失效该 attempt 并拒绝输入，再尽力关闭；文案只确认本地停止连接，明确服务端可能仍在收尾，不承诺 Root 命令取消或服务器名额已释放。
- 本地 retiring socket 在 onclose 前保留 owner，禁止自动重连；手动重连准入不能依赖删除旧 UI。明确呈现关闭中；浏览器不报告关闭时不得用超时伪造已释放。服务端仍可能晚于 onclose 清理，容量拒绝只给手动重试提示。
- started 只允许当前 connecting 接受一次；error/closed/onclose/超时均立即取消粘贴并禁止输入，清理 callback 身份。当前实例的能力、主题、Shell 行为、正常输出和 10 秒握手不变。
- captureSession signal 的 abort 同步停输入并清理全部资源；卸载/pagehide 同样处理；返回页面无自动连接。每次独立 try/finally 释放所有资源，即使其中一个动作失败；保持一次 disposal，不发送额外控制字符。
- P 片不引入输出水位/流控策略，只需安全接住现有 write 抛错并终止当前 owner。50MB 依赖水位与未来 256KiB 应用队列方案属于不同范围，不把 P 片描述为已解决浏览器内存预算。

### 有界、无真实终端的 mock 回归

- 使用 fake timers、至多两个 fake socket、fake Terminal/ResizeObserver；保存全部 callbacks，close 事件手动触发。不创建真实 WebSocket/listener/PTY，不执行命令，不用 sleep 等外部状态。
- R1：点击连接后立刻取消/卸载/abort，等待固定 nextTick；断言不产生晚到 socket/terminal、状态不复活、刷新结果不影响连接。分别覆盖 DOM 仍存在与已经卸载。
- R2：首连接关闭并收到 close 后手动重连；逐个调用保存的旧 message(started/error/closed/invalid JSON/binary)、error、close、timer、onData/onResize；新 socket 不收到任何 send/close，新状态、能力、timer、焦点不变。
- R3：connecting 按钮确实可取消，目标与容器选择锁定；重复连接只构造一次；取消后 started 不复活；closing 未收到 close 时不能再连接，close 后可手动再试。
- R4：真实 mock OPEN 流程；send(close)、socket.close、xterm 构造/open/fit/write/dispose 各抛错时其余可释放资源仍释放、输入立即失效、无未处理拒绝；重复 cancel/abort/unmount 幂等。
- R5：beginSession/invalidateSession、pagehide、卸载逐一验证；在同一同步调用栈再次触发输入/粘贴/resize，必须零发送；late nextTick 焦点回调无作用；恢复只显示未连接。
- R6：started 重复帧、10 秒边界超时后 late started、自然 close 清待确认粘贴、error 后旧输出无写入；能力为 false 时 resize 仍不发送；保留已有减少动效测试。
- R7：纯 DOM 键盘验证取消按钮名称/可达性、状态 polite、取消后合理焦点；针对新增控件做窄屏 fixture，保持原快捷键/多行确认语义。仅纯 mock 测试不能证明服务端早期启动已取消。

P 片完成标准：上述源码缺陷与 mock 回归闭环、前端类型检查/构建及主任务指定 CI 通过；不宣称 M15 多会话完成、不改当前 roadmap 的其他任务状态。


### P 片实施与验证记录（2026-10-13）

- `TerminalView.vue` 使用单次 attempt owner 管理准确 socket、xterm、FitAddon、订阅、观察器与计时器，连接前捕获目标快照并在异步边界校验页面、认证和 attempt 身份。旧回调不能操作新连接；`started` 仅接受一次。
- 连接中提供“取消连接”，connecting/connected/closing 均锁定目标。先同步撤销输入、粘贴与 resize，再逐项安全清理；认证失效、pagehide 和卸载同样处理。退役时解绑 message/error，保留准确 onclose 完成本地关闭；浏览器不报告 close 时继续显示关闭中，不自动重连或推断服务端容量已释放。
- 保留 10 秒握手、5000 行 scrollback、80ms resize debounce、能力处理、正常输出、显式键盘/粘贴交互和动态减少动效。初始化、send/close、write/dispose 等异常有终止与清理路径；聚焦为尽力操作，失败不重放输入。没有标签页、后端容量或协议修改，也没有新增输出水位策略。
- `TerminalView.test.ts` 的 R1–R7 共 **28 项纯 fake 测试通过**，使用 fake WebSocket/Terminal/ResizeObserver/timers；覆盖晚到事件隔离、取消及目标锁定、异常清理、认证/页面同步撤销、握手边界、正常输入/输出/resize、显式多行确认、减少动效与 DOM 焦点。前端 `vue-tsc --noEmit -p tsconfig.app.json`、两个实现/测试文件的定向 ESLint、diff 空白检查通过；独立源码复审已完成。
- `frontend/e2e/terminal-attempt-ownership.spec.ts` 新增 **6 项浏览器 fixture**，使用注入的 fake WebSocket，覆盖交互及窄屏场景；当前仅完成静态检查，尚未在本地启动浏览器/服务运行，运行结论待既有隔离 CI。不得把 fixture 存在或静态通过写成浏览器验收已通过。
- 主任务独立全量61文件964项单元、lint、类型检查、生产构建及540KiB预算通过，最终浏览器 CI 待验。不代表部署或 NAS 实机验收；远端命令退出、服务端资源立即回收、多会话容量和未来 M 片输出预算均不在本次完成范围。
