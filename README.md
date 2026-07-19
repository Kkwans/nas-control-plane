# NAS Control Plane

`nas-control-plane`（简称 NCP）是面向绿联 NAS 的本地服务器控制平面。它采用“非特权 Server + 特权 Agent”的分层架构：浏览器和 Server 不直接持有 Docker Socket 或宿主机 root 权限，受控宿主机能力只经由 Agent 的白名单接口提供。

## 当前状态

项目处于 Spec 定义的 **Phase 0：技术验证**。**P0-01 环境探测器已完成并在 DH4300 Plus 上进行只读实机核验**；下一项为 P0-02 Docker PoC。当前不部署 systemd 服务、不修改 Docker 状态，也不提供完整管理 UI。

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

同步副本不含真实 Git 元数据，因此本地验证显式关闭 VCS build stamp；NAS 实际仓库中的发布构建仍保留 VCS 信息。实际 NAS 探测属于只读验证；安装 Agent、创建 systemd 单元或访问 Docker API 将在对应 PoC 设计完成并单独确认后进行。
