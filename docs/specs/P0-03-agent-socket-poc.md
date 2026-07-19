# P0-03 Agent Socket PoC

## 目标

验证 root `ncp-agent` 可以通过 systemd 在 NAS 上监听 Unix Socket，并让非 root `ncp-server` 经 gRPC 调用唯一的只读探针 RPC。该 PoC 仅验证权限分层和本地 IPC，不实现 Docker 管理、终端、用户系统或完整 Server。

## 契约与范围

- Socket 路径固定为 `/run/ncp/agent.sock`，禁止 TCP 监听和远端端点配置。
- Socket 父目录权限为 `0750`，Socket 权限为 `0660`；父目录和 Socket 的组均为指定 Server 组。当前 NAS PoC 使用仅包含当前 SSH 账户的既有 `users` 组，正式部署必须替换为专用服务组。
- 唯一 RPC 为 `AgentProbeService/GetStatus(google.protobuf.Empty)`，返回 `google.protobuf.Struct`，其中只允许 `protocol_version`、`agent_euid` 和 `transport` 三个字段。
- Agent 不接受任意命令、镜像、容器、文件路径或网络地址，也不访问 Docker Socket。
- `ncp-server` 只可连接固定 Unix Socket；超时或连接失败必须返回稳定错误码。

## 验收标准

- 以 root 运行的 systemd Agent 单元成功创建 `/run/ncp/agent.sock`。
- Socket 的实际模式为 `0660`，父目录模式为 `0750`，所属组为用于非 root Server 的专用组。
- 以 `users` 组内非 root 用户启动的 `ncp-server agent-probe` 能获得 `protocol_version`、`agent_euid=0` 和 `transport=unix`。
- 不属于该组的用户无法连接 Socket。
- Agent 进程退出后 Socket 被清理；PoC 停止后不保留临时 unit、二进制、用户、组或 `/run/ncp` 目录。

## 回滚与验证

- 影响范围仅为临时 `ncp-agent-p0.service`、`/opt/ncp-p0` 与 `/run/ncp`；不创建或修改系统用户和组。
- 实机运行前先确认上述路径、unit 和系统身份不存在，且当前 SSH 账户具有非交互 root 权限；若无该权限，只完成代码与本地验证，不尝试绕过系统权限。
- 回滚顺序：停止并禁用临时 unit，删除临时 unit 和二进制，执行 systemd daemon-reload，删除临时 Socket 目录。
- 本地验证：`go test ./...`、`go vet ./...`、Linux ARM64 交叉构建；实机验证还需检查 systemd 进程 UID、Socket 属主/模式、组内非 root 调用和清理状态。

## 可追溯性

- ADR：[ADR-0003](../adr/0003-p0-agent-socket-boundary.md)
- Agent RPC 契约：[Agent Socket Proto](../../api/proto/ncp/agent/v1/agent_socket.proto)
- 原始 Spec：Phase 0 / P0-03、4.1 架构、10 安全底线、16 Codex 实施约束。

## 交付状态

- 状态：代码完成，root 实机验证待 NAS 提权能力。
- 2026-07-19 已通过 `go test ./...`、`go vet ./...` 和 Linux ARM64 静态交叉构建；gRPC 内存往返、稳定错误码、Socket 生命周期、Agent 入口和非特权 Server 客户端均有测试覆盖。
- NAS 预检确认 `/run/ncp/agent.sock` 不存在，当前 SSH 账户 UID 为 1000 且属于 `users` 组；`sudo -n` 不可用，因此未安装 unit、未写入 `/opt` 或 `/run`、未创建任何服务账号，也未尝试绕过系统权限。
