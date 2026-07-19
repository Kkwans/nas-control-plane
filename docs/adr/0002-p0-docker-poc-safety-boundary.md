# ADR-0002：P0-02 Docker PoC 仅操作带标签的临时容器

- 状态：已接受
- 日期：2026-07-19
- 决策范围：P0-02 Docker PoC

## 背景

NCP 的 Agent 需要验证 Docker 容器列表、Inspect、stats、events、logs、exec 与 image pull 能力，但 NAS 上已有运行中的生产服务。任何 PoC 都不能把这些服务作为测试对象。

Docker 官方当前建议新项目使用 Moby 的拆分 Go SDK 模块 `github.com/moby/moby/client`，并在新项目中启用 API 版本协商。[Docker Engine SDK 文档](https://docs.docker.com/reference/api/engine/sdk/)

## 决策

1. P0-02 只使用 `ncp-agent` 内部的 Docker SDK 适配器，并固定连接本机 `/var/run/docker.sock`；Server 尚未实现、也不能直连 Docker Socket，Agent 也不读取 `DOCKER_HOST` 等环境变量改写目标端点。
2. PoC 的目标容器必须同时满足名称 `ncp-p0-docker-poc` 与标签 `ncp.poc=docker`；不满足任一条件即返回稳定错误码，不发起 exec、pause、unpause 或日志读取。
3. exec 命令固定为验证标记，镜像引用固定为 `docker.io/library/alpine:3.21`，不接受浏览器、RPC 或 CLI 传来的任意命令、镜像或 Docker 参数。
4. 事件验证仅对临时容器执行 pause/unpause；无论中间步骤成功与否，Runner 都尝试恢复为运行状态。
5. POC 结果只保留聚合指标、标记与事件动作，不持久化环境变量、原始日志、Docker inspect 全量内容或拉取流。

## 后果

- 该 PoC 可以在真实 NAS 上证明 SDK 通道可用，同时避免访问现有控制面容器。
- P0-02 不创建可复用的 Docker 管理 API；真正的容器控制能力仍须等待 P0-03 Agent Socket、鉴权、审计与 Job 框架。
- 实机测试需要一个带只读空目录挂载、自动清理的独立容器；测试结束后只处理该容器和本次创建的临时目录。
