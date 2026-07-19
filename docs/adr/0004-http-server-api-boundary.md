# ADR-0004：以受控 HTTP 适配层连接浏览器与 Agent

## 状态

已接受（Phase 0 前端与 Server 基础设施）。

## 背景

前端需要稳定的 HTTP 契约，才能从演示数据逐步迁移至 NAS 的真实状态。已有 Agent Socket PoC 验证了非特权 Server 与特权 Agent 的通信，但浏览器不能直接访问 Unix Socket，Server 也不能绕过 Agent 读取 Docker Socket 或宿主机文件。

## 决策

- 使用 `net/http` + `chi` 建立 `ncp-server serve` HTTP 入口，默认只监听 `127.0.0.1:8750`。
- HTTP 入口只暴露三个无副作用端点：`/healthz`、`/api/v1/system/agent-status` 和 `/api/v1/system/capabilities`。
- Server 通过已有 Agent Socket 调用 `Probe` 和 `CollectCapabilities`；不直接读取 Docker Socket、`/proc`、`/sys` 或调用宿主机命令。
- Agent 的新增 RPC 仅收集已有 P0-01 的只读能力模型，不增加任意命令执行、Docker 管理或写入能力。
- 每个 Agent 调用必须拥有请求级超时；HTTP 错误返回稳定 `code`、非敏感 `message` 和 `requestId`。
- 在此阶段不部署常驻 HTTP 服务、不开放外网或局域网监听、不加入认证、数据库、Job 或危险管理操作。

## 后果

前端可以先稳定对接只读系统能力，并在 Agent 不可用时显示明确降级状态。该决定不会提前解锁 Phase 1 的用户、会话、容器管理或终端功能；它们仍需各自的接口、权限、审计、测试和 NAS 实机验证。
