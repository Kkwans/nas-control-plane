# ADR-0001：Phase 0 环境探测器采用 Agent 侧只读探测

- 状态：已接受
- 日期：2026-07-19
- 决策范围：P0-01 环境探测器

## 背景

NCP 需要在绿联 NAS 上识别架构、Docker、Compose、cgroup、journald、设备、网络、温度和权限能力。Spec 明确要求 Web Server 不得直接访问 Docker Socket 或宿主机，也不允许以 root 身份运行。

## 决策

P0-01 的探测逻辑仅属于 `ncp-agent`，并且只执行读取操作：读取固定的系统信息文件、检查受控命令是否存在、执行带超时的版本查询。HTTP OpenAPI 与 gRPC Proto 在本阶段作为契约先行；实际 Unix Socket gRPC 传输留给 P0-03。

探测结果不落 SQLite。Docker、温度、磁盘等仍是实时事实来源，后续 Server 仅通过 Agent 获取快照。缺失的可选能力以布尔值、空列表或稳定 Warning Code 表示，不能使整个探测失败。

## 后果

- P0-01 可以在不部署 systemd 单元、不创建容器、不拉取镜像的前提下完成单元测试与 ARM64 交叉构建。
- `ncp-server` 在 P0-03 前不实现宿主机访问或临时直连逻辑。
- 真实 NAS 执行探测虽为只读，仍需在代码交付后单独记录实机结果；安装 Agent 或创建 Socket 属于运行时变更。
