# NCP 运行故障、移动端与验收体系修复

```yaml
plan_id: NCP-2026-08-01-runtime-responsive-recovery
version: 1
status: completed
owner: luna
```

## 目标

修复日志、系统详情和终端三条真实故障链，建立可复用的响应式界面基础，并让用户管理、菜单排序及站点/数据库/日志深层页面在移动端可用。全部模块完成后仅执行一次 NAS 部署。

## 范围与非目标

- 包含：日志时间与正文清理、系统详情错误分层与部分降级、终端握手错误反馈、公共响应式组件、用户与菜单排序、重点深层页面移动端布局、接口与文档同步。
- 不包含：OpenSpec 工作流、持续 reconcile 定时器、业务容器破坏性测试、OpenClaw 网关改动、与本计划无关的功能扩张。

## 当前事实与证据

| 事实 | 证据 | 等级 |
| --- | --- | --- |
| Windows 是同步源码副本，真实 Git/运行态在 NAS | `NAS-Project/AGENTS.md`、SSH 核验 | A |
| NAS 已启用 `ncp-stack.service`，未启用 reconcile 定时器 | systemd/SSH 核验 | A |
| 日志列表使用本地时区格式化，详情只替换 `T/Z` | `frontend/src/views/LogsView.vue` | A |
| 日志前缀只清理 level+RFC3339，未覆盖时间-only 前缀 | `internal/httpapi/logs.go` | A |
| 系统详情当前把所有 Agent 错误折叠为 503 | `internal/agentsocket/dashboard_service.go`、`internal/httpapi/handler.go` | A |
| 终端 pre-start close 可能停留在 connecting，缺少握手超时 | `frontend/src/views/TerminalView.vue`、`internal/httpapi/terminal.go` | A |

## 不变量与兼容

- 不改动现有脏的 README、OpenAPI、Compose、进度、设计文档和 `.sync-conflict-*` 副本；新增文档单独提交。
- 不暴露或保存凭据；不执行 `docker compose down`、清理、强制推送或历史重写。
- Agent 与 Server 最终使用同一 Git 提交构建；错误响应保留既有字段并增加可诊断错误码。
- 旧日志、旧任务和旧客户端响应继续可读；未知字段按降级策略处理。

## 任务顺序

1. 日志：统一时间模块，严格清理结构化前缀，补充 Go 回归用例。
2. 系统详情：映射协议不匹配、Agent 不可达、能力缺失和采集失败；保留部分数据与 warnings。
3. 终端：握手超时、错误帧、真实 rows/cols 协商和 pre-start 状态收敛。
4. 公共 UI：统一断点、工具栏、表格/卡片、弹窗、Tab、操作菜单和状态/焦点动效。
5. 用户与菜单：表格/卡片、局部更新、拖拽与键盘排序、自动持久化。
6. 移动端：站点、数据库列表/详情/数据表、日志及其它重点路由。
7. 聚焦检查、完整构建、文档更新、备份与一次性部署验收。

## 证据与交付

- 每个模块先运行聚焦检查，提交带中文正文的 Conventional Commit 并推送 NAS 实仓。
- 最终执行 `go test ./...`、ARM64 Agent/Server 构建、Vue 类型检查和生产构建；浏览器检查 1440/1024/768/390px 及 Firefox 缩放。
- 部署前备份 SQLite、Compose 和 Agent 二进制；部署后核验 systemd、Compose、API、静态资源、SSE、终端和菜单持久化。

## 风险与未验证项

- 当前 NAS 已运行的 Agent/Server 版本需在最终部署前与同一提交核对。
- 真实移动端浏览器和部分 Agent 采集能力只能在最终部署后验证；开发阶段不把编译结果当作运行态证据。

## 完成记录

- 日志修复已由 `9463703` 交付：统一浏览器本地时区格式化、列表秒级时间、详情高精度时间、结构化开头前缀清理、原始内容折叠和 Go 回归用例。
- 响应式修复已由 `536e0ec`、`8466a56`、`05074fe` 分批交付：站点窄屏行布局、共享工作区头部宽度和工作区可收缩网格；三次提交均已推送，NAS `HEAD` 与 `origin/main` 一致。
- 本地验证通过：`go test ./...`、`go vet ./...`、Vue Vitest 12/12、`vue-tsc` 和 Vite production build；Vite 仅保留既有大 chunk warning。
- NAS 使用同一提交 `536e0ec` 构建并部署 Agent/Server；运行中 Agent 为 AArch64 ELF，Server 镜像为 `sha256:cdf536f64baf...`，后续两个提交仅修改静态前端，未再次重启 Agent 或 Compose。
- 部署前备份位于 `/volume2/Project/nas-control-plane/_bak/deploy/20260802-1240-runtime-responsive-recovery`，包含 SQLite、Compose 和旧 Agent；旧镜像保留为 `nas-control-plane:2026.7.30-v1-backup-536e0ec`。
- NAS 验收通过：`ncp-agent.service`、`ncp-stack.service` active/enabled，Server/Console healthy，`/healthz` 返回 `ok`；系统信息页面显示真实设备详情，主机终端真实完成连接、握手和回收。
- 内置浏览器已登录会话下，1440/1280/1024/768/480/390 站点页均无横向溢出，390 下 12 条关键路由均无 alert；Firefox 缩放未执行，因为当前可用的是 Codex 内置浏览器。日志列表当前无记录，时间格式主要由源码回归用例覆盖。
