# P0-02 Docker PoC

## 目标

验证 `ncp-agent` 能通过 Docker 官方 Go SDK 与 NAS Docker Engine 通信，覆盖 Spec 要求的容器列表、Inspect、一次 stats、events、日志、受控 exec 与 image pull。该能力仅用于 Phase 0 架构验证，不是 Docker 管理功能。

## 前置与边界

- 目标 Docker Engine、API 版本、测试镜像和现有容器状态必须先通过 SSH 只读核验。
- Agent 固定连接 NAS 本机 `/var/run/docker.sock`，不读取 `DOCKER_HOST` 等环境变量改写目标端点。
- 实机测试容器固定命名为 `ncp-p0-docker-poc`，必须含 `ncp.poc=docker` 标签、只读空目录挂载与自动清理设置。
- 测试镜像固定为 `docker.io/library/alpine:3.21`；先确认没有现有容器依赖它，再执行 ImagePull。
- 不读取或持久化容器环境变量、挂载内容、原始日志或 Registry 凭据。
- 不对现有容器执行 exec、pause、unpause、start、stop、restart、删除或镜像操作。

## 受控行为

1. 使用 API 版本协商创建 Moby SDK Client。
2. 列出容器，并对受标签保护的临时容器执行 Inspect 与非流式 stats。
3. 仅读取该临时容器末尾日志的有限字节数。
4. 仅执行固定命令 `printf NCP_P0_EXEC_OK`，并只在临时容器标签校验通过后进行。
5. 完整消费 ImagePull 响应流，但只返回完成状态，不返回原始 Registry 输出。
6. 订阅该临时容器的事件，执行 pause/unpause 并确认收到 `pause` 事件；无论成功或失败都恢复运行状态。

## 验收标准

- Docker API 版本可读取，且 API Client 不接受未校验的目标容器。
- 列表、Inspect、stats、logs、exec、image pull 与事件订阅均返回成功结果。
- 目标容器名称或标签不匹配时，返回 `DOCKER_POC_TARGET_REJECTED`，且不执行任何控制动作。
- 调用方 Context 取消时，SDK 调用终止并返回可识别错误。
- 实机测试不会改变现有服务的状态；测试资源可被明确识别和回滚。

## 回滚与验证

- 影响范围：仅 `ncp-p0-docker-poc` 与其 `/tmp/ncp-p0-docker-poc` 空目录；不会挂载或修改项目数据。
- 备份：不需要，测试资源没有用户数据、配置或持久卷。
- 回滚：停止并移除测试容器（自动清理），删除本次创建的临时空目录；仅在确认无容器引用时才可删除测试镜像。
- 验证：`go test ./...`、`go vet ./...`、Linux ARM64 交叉构建，以及 NAS 上的 Docker POC JSON 结果和测试资源清理检查。

## 可追溯性

- ADR：[ADR-0002](../adr/0002-p0-docker-poc-safety-boundary.md)
- Agent RPC 契约：[Docker PoC Proto](../../api/proto/ncp/agent/v1/docker_poc.proto)
- 原始 Spec：Phase 0 / P0-02、4.2 Agent、10 安全底线、16 Codex 实施约束。

## 交付状态

- 状态：已完成。
- 2026-07-19 已通过 `go test ./...`、`go vet ./...` 和 Linux ARM64 静态交叉构建。
- 已在 DH4300 Plus 上通过 Docker API 1.54 完成列表、Inspect、一次 stats、有限日志读取、固定标记 exec、固定 `alpine:3.21` 镜像拉取及 pause 事件验证；返回 `NCP_P0_EXEC_OK` 与 `pause`，没有输出或持久化原始日志、环境变量、挂载内容或 Registry 凭据。
- 实机测试仅创建了名称和标签均受代码校验的 `ncp-p0-docker-poc` 容器，使用只读根文件系统、无网络、只读空目录挂载与 `--rm`；结束后已确认容器、临时二进制和临时目录均不存在，预先存在的 `alpine:3.21` 镜像保持不动。
