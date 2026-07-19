# NAS Control Plane

`nas-control-plane`（简称 NCP）是面向绿联 NAS 的本地服务器控制平面。它采用“非特权 Server + 特权 Agent”的分层架构：浏览器和 Server 不直接持有 Docker Socket 或宿主机 root 权限，受控宿主机能力只经由 Agent 的白名单接口提供。

## 当前状态

项目处于 Spec 定义的 **Phase 0：技术验证**。**P0-01 环境探测器、P0-02 Docker SDK PoC、P0-04 终端 PoC 与 P0-05 journald PoC 均已完成实机核验**；P0-03 的 Unix Socket gRPC 与只读 HTTP 适配层已完成代码、测试和 ARM64 构建，root systemd 实机验证仍待具备非交互提权条件后执行。P0-04 仅通过显式 `--terminal-poc` 参数开放受控验证通道，P0-05 仅通过 `journal-poc` 验证受控日志读取，二者都不能视为生产 Web 功能。Vue 前端工程、统一设计系统和只读页面骨架已建立，但尚未接入完整真实 API，也不代表完整管理 UI 或通用 Docker 管理接口。

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
