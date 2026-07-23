# NAS Control Plane 阶段进度

更新时间：2026-07-23

## 当前结论

NCP 已从 Phase 0 技术验证切换为 Root 控制面 MVP。当前代码已经形成“Root Agent → Unix Socket gRPC → Server HTTP API → Vue 控制台”的真实数据闭环；运行中的 NAS Compose 已包含 Server、Nginx 控制台和 SQLite 数据目录。

## 已完成

| 模块 | 状态 | 证据 |
| --- | --- | --- |
| Root Agent 实时系统与 Docker 数据 | 已完成 | gopsutil 主机快照、Docker Engine/容器/Compose 项目发现、Dashboard RPC 与受保护 HTTP API；Go 全量测试、vet、ARM64 构建通过。 |
| Root 登录与 SQLite 会话 | 已完成 | 一次性 Root 初始化、bcrypt 哈希、SQLite 会话、登录/登出/状态 API；不存在默认密码，首次账号由管理员在页面创建。 |
| Root Agent 实机部署 | 已完成 | NAS systemd 单元已启用且 active；Unix Socket 为 root:users；`agent-probe` 返回 `agentEUID=0`、`transport=unix`。 |
| Server + Console Compose | 已完成 | `nas-control-plane` 与 `nas-control-plane-console` 均为 healthy；Nginx `/api/` 已反代 Server；`/healthz` 返回 200。 |
| 浅色现代 Vue 控制台 | 已完成 | Vue 3 + TypeScript + Vite；Root 初始化/登录、总览、基础设施、Services 页面统一浅色设计系统、圆角、间距和交互反馈。 |
| 容器生命周期控制 | 已提交 | `feat(docker)：实现容器启停重启控制`；Root Agent、HTTP API、Services 操作条和单元测试已完成。 |
| 容器日志尾部读取 | 代码完成，待提交 | bounded tail、stdout/stderr 结构化条目、Agent RPC、HTTP API 与 Services 日志面板已实现，正在完成提交前验证。 |

## 已验证命令

后端：

```text
go test ./...
go vet ./...
go build -buildvcs=false ./cmd/ncp-agent ./cmd/ncp-server
```

前端：

```text
pnpm test                 # 8 tests passed
pnpm run typecheck
pnpm run build
```

上述命令均已在 Windows 同步副本执行；ARM64 交叉构建也已通过。同步副本不含有效 Git 元数据，真实提交、推送和 NAS 运行状态以 SSH 到 `/volume2/Project/nas-control-plane` 的结果为准。

## 当前运行态与边界

- NAS 当前运行的是已部署的 Server/Console 镜像；新提交的容器控制与日志代码尚未重建镜像或重启 Compose。
- `/api/v1/auth/status` 当前返回 `initialized=false`，需要在页面完成一次性 Root 账号创建后，才能验证登录态下的实时主机、Docker、容器控制和日志链路。
- 容器控制和日志接口都要求有效 Root 会话；本阶段没有对 NAS 真实容器执行启停、重启或日志读取。

## 下一步

1. 提交并推送容器日志切片。
2. 在确认运行时更新后，重建 ARM64 Server 镜像并滚动重建 Compose，做只读健康检查和认证状态检查。
3. 管理员在控制台创建 Root 账号，完成登录后验证真实总览、Services 操作条和日志面板。
4. 继续交付主机/容器终端与耗时操作 Job；之后再处理 Caddy、备份恢复和更细粒度权限。
