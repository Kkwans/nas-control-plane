# P0-01 环境探测器

## 目标

在不改变 NAS 状态的前提下，由 `ncp-agent` 输出 NCP 后续模块需要的环境能力快照。该功能对应原始 Spec 的 P0-01，并为 `GET /api/v1/system/capabilities` 和未来 Agent RPC 提供同一数据契约。

## 范围

本能力包包含：

- 系统、CPU 架构、设备型号与网卡名称的只读采集。
- Docker Socket、Docker API 版本与 Docker Compose 的可用性判断。
- systemd、journald、cgroup 版本、`/proc` 与 `/sys` 可读性判断。
- `smartctl`、`nvme-cli`、温度节点、数据卷、系统用户管理能力、根文件系统可写性与主机终端前置条件判断。
- 对外 OpenAPI 与预留 gRPC Proto 契约。

不包含：systemd 安装、Unix Socket 建立、gRPC 服务启动、Docker 容器操作、镜像拉取、终端创建、数据库、前端或持久化。

## 行为与验收

1. 所有探测均为只读；不得创建文件、用户、容器、镜像、Socket 或数据库记录。
2. Docker 不可用、温度节点不存在、可选工具缺失时，仍返回完整能力对象并给出 `false`、空值或稳定 Warning Code。
3. 外部命令只能是受控版本查询，必须继承调用方 Context 并使用超时。
4. cgroup v2 优先于 v1 识别；无法识别时返回 `0`。
5. `/proc/mounts` 中根挂载为 `ro` 时，`rootFilesystemWritable` 必须为 `false`。
6. 单元测试必须覆盖完整 ARM64 NAS 样例、可选能力缺失样例与只读根文件系统样例。

## 可追溯性

- ADR：[ADR-0001](../adr/0001-phase-0-capability-probe-boundary.md)
- HTTP 契约：[OpenAPI](../../api/openapi/openapi.yaml)
- Agent 契约：[Proto](../../api/proto/ncp/agent/v1/capabilities.proto)
- 原始 Spec：Phase 0 / P0-01、12 绿联 NAS 适配策略、16 Codex 实施约束。

## 验证计划

先通过伪造平台的单元测试验证所有判断分支，再在 Windows 开发机运行 `go test ./...`、`go vet ./...` 和 Linux ARM64 交叉构建。实机运行仅在交付后的只读核验中执行；任何 systemd、Docker 或 Socket 操作不属于本能力包。

## 交付状态

- 状态：已完成。
- 2026-07-19 已通过 `go test ./...` 和 `go vet ./...`，并完成 Linux ARM64 静态交叉构建。
- 已在 DH4300 Plus 上临时运行探测二进制，确认 ARM64、Debian 12、Docker、Compose、systemd、journald、cgroup v2、温度节点及 `/volume1`、`/volume2` 数据卷根均可正确识别，返回结果没有 Warning。
- 实机结果发现 Docker overlay 子挂载会混入数据卷列表；已以失败测试复现并修复为只返回数据卷根路径。
- 本次核验没有安装服务、创建 Agent Socket、创建/控制 Docker 资源或修改 NAS 配置。
