# NAS Control Plane

`nas-control-plane`（简称 NCP）是面向绿联 NAS 的本地服务器控制平面。它采用“非特权 Server + 特权 Agent”的分层架构：浏览器和 Server 不直接持有 Docker Socket 或宿主机 root 权限，受控宿主机能力只经由 Agent 的白名单接口提供。

## 当前状态

项目处于 Spec 定义的 **Phase 0：技术验证**。**P0-01 环境探测器与 P0-02 Docker SDK PoC 均已完成实机核验**；下一项为 P0-03 Agent Socket、鉴权、审计与 Job 框架。当前不部署常驻 systemd 服务、不提供完整管理 UI，也不提供通用 Docker 管理接口。

## 目录

- `api/openapi/`：对外 HTTP API 契约。
- `api/proto/`：Server 与 Agent 间的 RPC 契约。
- `cmd/ncp-agent/`：Agent 可执行入口。
- `internal/system/`：只读环境能力探测实现。
- `docs/adr/`：架构决策记录。
- `docs/specs/`：可追溯的功能规格与验收标准。

## 本地验证

```powershell
go test ./...
go vet ./...
go build -buildvcs=false ./cmd/ncp-agent
```

Linux ARM64 交叉构建：

```powershell
$env:GOOS = 'linux'
$env:GOARCH = 'arm64'
$env:CGO_ENABLED = '0'
go build -buildvcs=false -trimpath -o bin/ncp-agent-linux-arm64 ./cmd/ncp-agent
```

同步副本不含真实 Git 元数据，因此本地验证显式关闭 VCS build stamp；NAS 实际仓库中的发布构建仍保留 VCS 信息。P0-02 的 Docker 访问仅针对带固定标签的临时容器完成验证并已清理；安装常驻 Agent、创建 Unix Socket 或 systemd 单元仍留待后续阶段。
