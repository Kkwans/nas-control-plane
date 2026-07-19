# NAS Control Plane

`nas-control-plane`（简称 NCP）是面向绿联 NAS 的本地服务器控制平面。它采用“登录后的 Root 控制台 + 原生 Root Agent”架构：`ncp-agent` 以 root 身份访问 Docker、systemd、journald、宿主机指标和 PTY；浏览器通过 `ncp-server` 使用这些能力。MVP 的 `root` 角色拥有所有已实现模块的完整权限。

## 当前状态

项目已完成可复用的 Phase 0 验证基础，并切换到 **Root 控制面 MVP** 交付节奏。**P0-01 环境探测器、P0-02 Docker SDK PoC、P0-04 终端 PoC 与 P0-05 journald PoC 均已完成实机核验**；P0-03 的 Unix Socket gRPC 与 HTTP 适配层已完成代码、测试和 ARM64 构建，下一优先级是将 Root Agent 作为 systemd 服务真正部署并接入 Server。P0-06 Caddy 路由持久化代码可保留为后续能力，不再阻塞首页、Docker、服务发现、日志与终端的真实数据交付。Vue 前端是 Vue 3 SPA，不是手写静态 HTML；当前已部署版本因 API 未接通而展示预览数据，必须尽快替换为真实 API。

## 目录

- `api/openapi/`：对外 HTTP API 契约。
- `api/proto/`：Server 与 Agent 间的 RPC 契约。
- `cmd/ncp-agent/`：Agent 可执行入口。
- `cmd/ncp-server/`：非特权 Server 的最小 Agent 客户端入口。
- `internal/agentsocket/`：受限 Unix Socket gRPC 通道实现。
- `internal/httpapi/`：只读 HTTP API、请求 ID 与稳定错误响应。
- `internal/system/`：只读环境能力探测实现。
- `frontend/`：Vue 3 + TypeScript + Vite 控制台与测试工程。
- `docs/adr/`：架构决策记录。
- `docs/specs/`：可追溯的功能规格与验收标准。

## 本地验证

```powershell
go test ./...
go vet ./...
go build -buildvcs=false ./cmd/ncp-agent
go build -buildvcs=false ./cmd/ncp-server
```

前端验证：

```powershell
pnpm -C frontend test
pnpm -C frontend run typecheck
pnpm -C frontend run build
```

Linux ARM64 交叉构建：

```powershell
$env:GOOS = 'linux'
$env:GOARCH = 'arm64'
$env:CGO_ENABLED = '0'
go build -buildvcs=false -trimpath -o bin/ncp-agent-linux-arm64 ./cmd/ncp-agent
go build -buildvcs=false -trimpath -o bin/ncp-server-linux-arm64 ./cmd/ncp-server
```

同步副本不含真实 Git 元数据，因此本地验证显式关闭 VCS build stamp；NAS 实际仓库中的发布构建仍保留 VCS 信息。P0-02 的 Docker 访问仅针对带固定标签的临时容器完成验证并已清理；安装常驻 Agent、创建 Unix Socket 或 systemd 单元仍留待后续阶段。HTTP Server 默认仅监听本机回环地址，不会自动部署或开放端口。

P0-04 的端到端验证、容器保护条件与关闭协议见 [P0-04 Terminal PoC](docs/specs/P0-04-terminal-poc.md)。

P0-05 的 journald 查询、Cursor、筛选、Follow 与实机验证边界见 [P0-05 journald PoC](docs/specs/P0-05-journald-poc.md)。

阶段成果、已核验证据和待办见 [阶段进度](docs/PROGRESS.md)。Root 控制面 MVP 的架构调整见 [v0.2 方向说明](docs/specs/v0.2-root-control-plane-direction.md)。`deploy/console/` 中现有 Compose 项目仅是前端预览入口，不代表完整 NCP 发布形态。
