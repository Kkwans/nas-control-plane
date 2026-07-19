# ADR-0003：P0-03 Agent 仅通过固定 Unix Socket 暴露只读探针

- 状态：已接受
- 日期：2026-07-19
- 决策范围：P0-03 Agent Socket PoC

## 背景

NCP 的 Server 必须保持非 root 身份，Docker Socket 和宿主机高权限能力只能由 Agent 持有。P0-03 需要先证明 root Agent、systemd、Unix Socket、gRPC、非 root Server 调用和 Socket 权限能够在 NAS 上协同工作，不能提前扩展为管理 API。

## 决策

1. `ncp-agent serve` 仅监听固定路径 `/run/ncp/agent.sock`，只使用 Unix Domain Socket，不提供 TCP 监听或可配置远端端点。
2. P0-03 仅公开 `AgentProbeService/GetStatus`。它使用 protobuf well-known 类型 `Empty` 与 `Struct`，只返回协议版本、Agent 有效 UID 和传输类型；不调用 Docker、不执行外部命令，也不接受命令、路径或资源标识输入。
3. Agent 在创建 Socket 前将 `/run/ncp` 目录赋予指定的 Server 组并设为 `0750`，Socket 设为 `0660`。root Agent 保持所有者，只有该组内的非 root Server 进程可以连接。
4. Server 仅以 `unix:///run/ncp/agent.sock` 作为 gRPC 目标，使用本地 IPC 传输凭据；鉴权边界由 Unix 文件系统权限提供，不为本机 Socket 额外启用 TCP/TLS。
5. systemd 单元明确以 root 启动 Agent。当前 NAS 没有可用的标准化系统组创建工具，因此实机 PoC 使用仅包含当前 SSH 账户的既有 `users` 组验证权限；RPC 只读且验证后应停止、禁用并清理临时二进制、单元文件和运行时资源。正式部署前必须替换为专用 `ncp-server` 组。

## 后果

- P0-03 可以验证最关键的提权边界，而不会暴露通用宿主机控制能力。
- Socket 组权限不是完整 RBAC；用户、会话、审计和 Job 持久化仍属于后续 MVP 阶段。`users` 组仅可用于这次无危险 RPC 的 PoC，不能作为生产部署的最终授权边界。
- 仓库当前没有 `protoc` 工具链，PoC 的 Go gRPC 服务描述以等价的静态绑定实现，并以 `.proto` 文件作为规范来源；引入稳定的代码生成工具链后，应迁移至生成绑定而不改变 RPC 名称和消息语义。
