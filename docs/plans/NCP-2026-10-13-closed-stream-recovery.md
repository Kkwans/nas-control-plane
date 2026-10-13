# 已关闭实时流的显式只读恢复

2026-10-13，基线 f633432。

## 已确认问题

系统 SSE 遇到坏 JSON、整个信封或摘要结构时会关闭 EventSource。共享 store 保留源引用及新鲜度时钟，同作用域启动直接返回；顶部刷新虽能通过 GET 接受新数据，却不能重建 CLOSED 流。可见性恢复与同作用域导航也不会重建，只有改变作用域或真正重新登录等生命周期变化可恢复。

这与浏览器原生 CONNECTING 重连不同，也不包括 M29 已隔离的 Docker 坏切片；Docker 坏切片继续沿用同一源接收后续快照。

## 最小范围与不变量

- 顶部显式刷新在首个 await 之前调用 store 的 CLOSED-only 恢复入口，保留点击互斥与当前认证检查。
- 仅在页面可见、有仍被 store 持有的 CLOSED 源、且有实时作用域时重建。OPEN/CONNECTING、隐藏页、空作用域、stop/clear/logout/dispose 后均不重建。
- 复用 stop/start 的单一源、时钟及代次清理；会话 abort 同步释放源，旧源回调不能写入新流。缓存和源采集时间保留。
- 不把恢复挂到时钟、可见性事件、普通 GET 或操作后强刷；不增加自动重试、轮询、写操作或任务重放。
- GET 成功、open、重复或过期 SSE 不能清除断流错误；只有新鲜且被接受的 SSE 样本可以证明恢复。
- M29 的 Docker 独立错误、GET-only 页面本地过期时钟、刷新复用与强制刷新结果屏障保持不变。

## 验证计划

1. readyState-aware fake EventSource 覆盖三种终止坏帧、用户重试、重复点击及二次失败，不用未来回调假装 CLOSED 会自然重连。
2. 覆盖原生 CONNECTING、坏 Docker 切片、隐藏与所有清理边界、空作用域、旧源迟到回调。
3. 覆盖只读 GET 更新仍保留断流错误、只有新鲜 SSE 恢复，以及普通/强刷并发与失败结果。
4. 挂载 App 的纯 jsdom 测试覆盖顶部入口、首个 await 前调用、重复点击和会话/导航期间晚到结果不触发恢复。
5. 复跑相关 API/store/手动刷新测试、类型检查、lint 与生产构建；不启动浏览器、监听端口、PTY、Docker 或 NAS。

## 实际证据

21:12 UTC 定稿，共 5 个文件：App/store 两处生产文件、两个新测试文件及本方案。修复保留同作用域启动与可见性行为；新增入口只在用户点击顶部刷新时检查 CLOSED。生产改动期间开发分支推进到 73e1064，既有 M29 行为不变。

- 新增 20 项纯测试：store 16 项，App 挂载 4 项。覆盖三种坏帧、浏览器原生终止 CLOSED、CONNECTING、坏 Docker、重复/过期/逆序/未来样本、重复点击、二次坏帧、隐藏、空作用域、stop/clear/logout/dispose、旧回调、pending GET 期间导航/退出及强刷自身失败。
- 最终聚焦命令 `node node_modules/vitest/vitest.mjs run src/stores/system.realtimeRecovery.test.ts src/App.realtimeRecovery.test.ts src/stores/system.inventory.test.ts src/stores/system.test.ts src/api/dockerInventoryApi.test.ts src/api/system.test.ts src/composables/manualRefresh.test.ts`：7 文件 / 133 项全部通过。
- `vue-tsc --noEmit -p tsconfig.app.json`、全量 `eslint . --max-warnings=0`、`git diff --check` 通过。
- Vite production build、dist 权限规范化及 bundle budget 通过；最大 chunk 为 518.0 KiB，低于 540 KiB 预算。保留既有大于 500 kB 的提示，未将提示误报为构建失败。
- 独立复核无阻塞；最终六个相关套件 120 项通过，作用域 lint 通过。全工作区存在其他并行改动；精确提交快照的完整单元与静态检查由主任务发布前另行执行，不以混合工作区结果冒充该快照证据。

所有流、请求、时钟与 DOM 交互均为内存 fake/jsdom；没有启动真实浏览器或端口监听，没有 PTY、Docker、NAS、提交、推送或部署操作。视觉与线上运行验收未执行。

主任务发布前以 73e1064 为基线、仅叠加本模块五文件的精确快照验证：85 文件 / 1624 项单元、全量 lint、类型检查、生产构建与包预算全部通过。此处不含并行日期提示框改动。

## 浏览器 fixture 补充（待 CI 运行）

2026-10-13 21:42 UTC，在 704b879 上补齐 `frontend/e2e/closed-stream-recovery.spec.ts`，共 8 项。独立 EventSource fixture 区分 OPEN、原生 CONNECTING、原生 CLOSED 与三种坏帧终止；关闭后普通帧不再投递，迟到回调显式注入。通过实际顶部按钮和导航/退出控件覆盖请求挂起期间终止、连续点击、只替换一个源、GET/open/重复帧不清错、新鲜 SSE 恢复，以及路由/会话清理。路由切换允许同会话 GET 完成更新缓存，但不得重启旧订阅；退出后的 GET 不得恢复已清空状态。等待 store 完成响应处理后再核对清理结果，避免把 route.fulfill 当作应用已处理。

所有 API 均在内存响应，外部来源、未列入清单的请求和 WebSocket 均拒绝；唯一允许的写请求为模拟退出。390/1440 两种宽度预备输出断流与恢复 PNG、完整 axe、控件几何、前后 store/源/请求/时钟状态和源码摘要。axe 仅推进最多 2 秒受控时钟；仅放行已关闭 about:blank 汇总页且主页面/上下文仍存活的特定 clock 错误，其他错误继续失败。

验证：在 frontend 目录执行 standalone `tsc --noEmit --strict --noUncheckedIndexedAccess --skipLibCheck --target ES2022 --module ESNext --moduleResolution Bundler --types node e2e/closed-stream-recovery.spec.ts`、该文件 ESLint（零 warning）、Playwright `--list`（8 项）及 `git diff --check` 均通过。本轮没有运行浏览器、监听器、PTY、NAS 或本地服务；上述截图、axe 与 E2E 通过状态仍须 CI 生成和核验，不能将静态发现等同运行验收。

主任务精确快照（704b879 加本片浏览器与 QA 文档）通过 86 文件 / 1654 项单元、全量 lint、Vue 类型检查与八项浏览器发现；独立严格 E2E 类型检查通过。实际浏览器运行仍待提交后 CI。

### 首轮 CI 修正与独立可访问性待办

实际 CI `38472748082`（a9c754）新增 8 项中 5 通过、3 失败：390/1440 两项已完成断流和恢复截图，但完整 axe 均报告 `region`（moderate），节点分别为 `#el-popper-container-672` / `#el-popper-container-4740`；退出项使用了不存在的“登录”按钮名，实际 LoginView 标题为“进入 NAS Control Plane”、按钮为“进入控制台”。本轮仅修正测试：退出后确认真实标题/控件；截图前将指针移开，再推进受控时钟直至 popper 完成正常离场。原先仅推进 34 ms，短于 Element Plus 默认 200 ms hideAfter，可能将短暂提示层冻结在截图与 axe 扫描之间。没有删除/隐藏 DOM、修改产品或排除 axe 规则，扫描后仍确认未重新出现 popper。

body 下可见提示层触发的 `region` 仍是独立、未关闭的可访问性发现。本片只验证指针移开后的稳定流状态，不声称覆盖或修复 hover 状态。日志只给出容器节点，未含提示文字；具体内容需结合该次 artifact 进一步核验。修正后 standalone 严格 TS、文件 ESLint、8 项发现及 diff check 通过；浏览器结果必须由后续实际 CI 确认。

捕获修正的独立精确快照（9f7acee 加两文件）通过 86 文件 / 1655 项单元、全量 lint 和 Vue 类型检查；八项 E2E 严格类型/发现通过。实际运行待后续 CI。

### 实际 CI 与四幅状态截图闭环（2026-10-13 22:14 UTC）

精确提交 `a7ba4bd781cf05598e72dac112e8c4fb0f3ea424` 的 [CI 38474203875](https://github.com/Kkwans/nas-control-plane/actions/runs/38474203875) 已成功：86 文件 / 1655 项单元，359 / 359 项 Chromium（7.9 分钟），Go 与 Compose 全部通过；ARM64 发布跳过。本模块八项浏览器 fixture 均实际通过，包括挂起 GET 期间路由切换及退出登录清理。

从 [官方分片 1 工件 11854325120](https://github.com/Kkwans/nas-control-plane/actions/runs/38474203875/artifacts/11854325120) 下载 10,252,845 字节 ZIP，低于 32 MiB；实际 ZIP SHA256 为 `ed7f2fa3aa326da04881bcba50f7fe7dd82ca85a728f0917b7665d71f66ed67a`，与官方摘要一致。逐幅查看 390px / 1440px 的 `closed-after-get`、`fresh-sse-recovered` 四幅 PNG，并读取四份完整状态 JSON；四份 `sourceSha` 均匹配上述提交，截图 SHA256 均与实际 PNG 字节一致，四份完整 axe 的 `violations` 为空，`denied` 与 `pageErrors` 均为空。

断流画面保留 CPU 31.0%、12:00:01 更新时间及“实时连接中断”，只读 GET 没有伪装流恢复；新鲜 SSE 画面更新为 CPU 42.0%、12:00:02 及“运行稳定”，状态记录分别为 offline / streaming。两种宽度的可见提醒、摘要和顶部恢复按钮可读，没有观察到水平裁切。移动截图为当前视口，未据此声称已目视检查屏幕下方所有卡片。

此闭环只覆盖指针移开后的稳定流状态。此前 `9f7acee8dc88cdf492fb4858ee61306cec2fe98a` 的四幅官方图片中，顶部刷新按钮旁确实显示“立即刷新数据”；其 body popper 对应原始 region 违规。a7 的稳定截图通过不代表该 hover 提示层缺陷已经修复；后续产品修复需以其自身精确提交的开态截图和 axe 另行验收。全部请求、流与退出均为隔离内存 fixture，没有操作真实账号、Docker、NAS，也没有部署验证。

## 完整 axe 调度预算修复（2026-10-13 23:55 UTC）

2534026的CI38485401492保留为380浏览器通过/1失败：390px坏JSON关闭流场景在首次采集的完整axe中，125次16ms页面时钟推进后仍未完成。该2000ms是合成时间，不等于Node/Chromium注入、跨进程返回或汇总页收尾的真实调度期限。纯Promise复现确认旧控制器可在不足1ms真实时间内耗尽全部页面预算，错误拒绝20ms后正常完成的扫描。CI日志没有阶段明细，不能认定该次恰好卡在某个内部阶段；后代c5完整381浏览器通过不抹去此前失败。

提取两个采集入口共用的纯控制器，每次tick前允许最多16ms真实调度，提前完成即停止推进；页面累计仍最多2000ms。另设真实5000ms总期限，fake预算耗尽后只被动等待，卡住的tick同样受总期限约束。保留一个已关闭about:blank辅助页、原页面/上下文/浏览器存活及精确错误文本的窄竞争容忍，最多1000ms收尾且受总期限限制。不重放tick、不重置时钟、不屏蔽axe违规，原始完整结果和拒绝继续传播；清理自有timer/监听并消费迟到拒绝，不声称能取消底层已在途的Playwright操作。

33项纯调度/生命周期回归（原21加12）通过；曾发现E2E目录helper超出composite应用TS边界，已移至src/test-support并复验，未扩大应用配置。主任务最终五文件精确快照1859完整单元/92文件、全量lint、Vue类型通过；两个实际E2E使用方严格TypeScript及独立复审通过。产品运行代码、完整axe与页面状态/请求断言未改变；实际浏览器调度效果待本提交CI，不以纯测试替代浏览器证明。
