# P0-04 Terminal PoC

## 目标

验证 NCP 的受控终端链路：浏览器 WebSocket、非特权 Server、Unix Socket 上的 Agent、Linux PTY 或受保护容器 TTY，以及 resize、Ctrl+C 和可靠终止。该 PoC 不实现生产终端管理功能。

## 范围与契约

- HTTP 路由为 `/ws/terminal?target=host|container`，只有 `ncp-server serve --terminal-poc` 显式开启时存在；常规 Server 入口不暴露该路由。
- 浏览器到 Server：二进制帧为终端原始输入；文本帧只允许 `{"type":"resize","rows":34,"cols":120}` 和 `{"type":"close"}`。
- Server 到浏览器：二进制帧为 UTF-8 终端输出；文本帧为 `started`/`closed` 会话状态。单帧输入上限 32 KiB。
- Agent gRPC 流使用 `google.protobuf.Struct` 的严格字段白名单。不存在 `exec(command)`、Shell、路径、用户、环境变量、工作目录或任意容器 ID 字段。
- 宿主机目标固定启动 `/bin/sh`，最小化 `HOME`、`PATH` 和 `TERM` 环境；会话上限为 1。
- 容器目标必须同时满足名称 `/ncp-p0-terminal-poc`、标签 `ncp.poc=terminal` 和运行中状态。exec 固定为非 privileged 的 `/bin/sh` TTY。
- 关闭容器会话必须发送 Ctrl+C 和 `exit`，半关闭输入，并在两秒内经 ExecInspect 观察到退出；否则返回失败而非留下后台 shell。

## 验收标准

1. 宿主机 PTY 能接收和返回 UTF-8 数据，`stty size` 能反映 34×120 resize。
2. Ctrl+C 能中断正在运行的 `sleep`，随后仍能执行固定验证输出。
3. WebSocket 能通过 Unix Socket Agent 把输入、输出和 resize 往返到真实 PTY。
4. 受标签保护的临时容器能运行相同的 TTY 验证；未带正确标签或名称的容器不会发起 exec。
5. 关闭后不能留下 `docker exec` shell；仅允许容器自身的测试守护进程存在。
6. 常规 `ncp-server serve` 不注册 Web 终端路由。

## 回滚与实机验证

- 不安装 systemd 单元、不持久化二进制、不修改 NAS 配置。
- 实机验证只创建隔离、无网络、无挂载、只读根文件系统的临时 `alpine:3.21` 容器，完成后删除该容器和 `/tmp/ncp-p0-terminal-20260719`。
- 2026-07-19：Linux ARM64 端到端测试 `TestTerminalWebSocketBridgesUnixAgentAndHostPTY` 在 DH4300 Plus 通过，覆盖 Unix Socket、真实 PTY、WebSocket、resize、Ctrl+C 和会话复用。
- 2026-07-19：`ncp-agent terminal-poc --target container` 在同一 NAS 通过；首次实测发现仅关闭 Hijacked Connection 会残留 `docker exec` shell，随后改为 Ctrl+C、`exit` 和 ExecInspect 退出确认。复测 `docker top` 只剩临时容器自身的守护进程。
- P0-03 的 root systemd/Socket 权限实机验证仍受非交互 sudo 不可用限制；本 P0-04 实机验证以当前 Docker 组内普通 SSH 用户运行，不将其表述为 root Agent 权限验证。

## 可追溯性

- ADR：[ADR-0005](../adr/0005-p0-terminal-poc-boundary.md)
- Agent RPC：[Agent Socket Proto](../../api/proto/ncp/agent/v1/agent_socket.proto)
- WebSocket 契约：[OpenAPI](../../api/openapi/openapi.yaml)
- 原始 Spec：6.7 Web 终端、9 WebSocket、10 安全底线、15 P0-04。
