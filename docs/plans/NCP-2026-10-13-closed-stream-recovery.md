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
