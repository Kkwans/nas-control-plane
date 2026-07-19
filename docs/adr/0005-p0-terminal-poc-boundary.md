# ADR-0005：P0-04 终端只验证固定会话与完整关闭路径

- 状态：已接受
- 日期：2026-07-19
- 决策范围：P0-04 Terminal PoC

## 背景

Spec 要求验证 PTY、WebSocket、resize、Ctrl+C、容器终端和会话终止，但完整的用户、RBAC、审计、Job 与终端 UI 尚未建立。直接把浏览器参数转发为 Shell、命令、容器 ID 或工作目录，会越过 NCP 的 Agent 白名单边界。

## 决策

1. P0 WebSocket 路由固定为 `/ws/terminal`，默认不注册；只有 `ncp-server serve --terminal-poc` 才启用，HTTP Server 仍默认监听 `127.0.0.1`。
2. 只接受 `target=host` 或 `target=container`。宿主机启动固定的 `/bin/sh`，采用最小环境变量；不接受 Shell、命令、工作目录、用户、环境变量或容器标识参数。
3. 容器目标固定为名称 `/ncp-p0-terminal-poc` 且标签 `ncp.poc=terminal` 的运行中临时容器。Docker exec 使用参数数组 `[/bin/sh]`、TTY 和标准流，保持非 privileged。
4. Server 与 Agent 通过新增的双向 gRPC 流传递 `start`、`input`、`resize`、`close`、`output` 和会话状态消息。消息字段白名单严格校验，二进制输入输出上限为 32 KiB；不记录终端输入或输出。
5. 容器会话关闭时必须依次向固定 Shell 写入 Ctrl+C 与 `exit`、半关闭输入、轮询 Docker ExecInspect 确认进程退出（2 秒上限）后关闭连接。任一步失败均使会话关闭失败，不能把存活的 exec 误报为已终止。

## 后果

- P0 可以完整验证关键 I/O 和生命周期路径，同时不把 Agent 扩展为通用命令 RPC。
- 容器会话关闭比单纯关闭 Hijacked Connection 更严格；实机验证曾发现后者会留下 `docker exec` shell，因此关闭确认是必要条件。
- 这不是生产 Web 终端：普通/root 用户选择、指定工作目录、容器选择、CSP、RBAC、审计、超时策略、会话列表与前端 xterm.js 仍属于后续 MVP。
