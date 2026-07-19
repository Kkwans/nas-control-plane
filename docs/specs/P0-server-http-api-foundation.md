# P0 Server HTTP API 基础

## 目标

建立浏览器可消费的、最小只读 HTTP 入口，并保持「浏览器 → 非特权 Server → 特权 Agent」边界。

## API

| 方法 | 路径 | 行为 |
|---|---|---|
| `GET` | `/healthz` | 返回 `ncp-server` 自身可响应状态，不访问 Agent。 |
| `GET` | `/api/v1/system/agent-status` | 通过 Unix Socket 查询 Agent 协议、EUID 和传输类型。 |
| `GET` | `/api/v1/system/capabilities` | 通过 Agent 返回 P0-01 的只读环境能力。 |

所有响应为 JSON，包含 `X-Request-ID`。Agent 不可用时，系统能力接口返回 HTTP `503` 和 `SYSTEM_CAPABILITIES_UNAVAILABLE`；状态接口返回 HTTP `503` 和 `AGENT_STATUS_UNAVAILABLE`。未知路由与不支持方法分别使用 `ROUTE_NOT_FOUND`、`METHOD_NOT_ALLOWED`。

## 输入与边界

- `ncp-server serve` 默认监听 `127.0.0.1:8750`，可显式指定 `--listen` 与 `--agent-socket`。
- Agent Socket 路径默认 `/run/ncp/agent.sock`。
- Server 不以 root 运行，且不直接访问 Docker Socket、宿主机文件或执行宿主机命令。
- Agent RPC 只允许读取能力模型，不开放任意命令执行。

## 验收

1. 健康检查返回 `200`、`status=ok`、`service=ncp-server` 与请求 ID。
2. 系统能力请求向 Agent 传递超时 Context，并原样返回已验证的能力模型。
3. Agent 不可用时，前端可依赖稳定错误码而不解析底层错误文本。
4. Agent Status 与 Capabilities RPC 均能在 gRPC bufconn 测试中调用。
5. `go test ./...`、`go vet ./...` 和 Linux ARM64 交叉构建通过。

## 非目标

- 用户初始化、登录、Session、RBAC、SQLite、Job、审计事件。
- 容器启停、镜像管理、终端、日志 Follow、反向代理修改等任何管理动作。
- 常驻部署、端口开放或反向代理接入。
