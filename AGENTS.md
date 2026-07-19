# NAS Control Plane（NCP）项目约束

## 当前产品方向

从 2026-07-19 起，NCP 的目标调整为**局域网内、登录后的 Root NAS 控制面**。Phase 0 已产出的能力验证作为实现依据，不再是交付真实页面和真实 API 的硬性前置条件。后续优先完成能读取和管理实际 NAS 资源的垂直切片，而不是继续扩展孤立 PoC。

NCP 的 Root 能力由原生 `ncp-agent` 提供：它以 root 身份运行，负责 Docker、systemd、journald、宿主机指标、文件系统和主机/容器终端。MVP 先提供一个登录后的 `root` 角色；该角色拥有所有已实现模块的完整读取和管理权限。多用户细粒度 RBAC 留到后续阶段，不阻塞 Root 控制台交付。

实际部署、启动、停止、重启或修改 NAS 运行环境时，以当次用户指令为准；不要把源代码同步误报为已部署。

## 架构约束

1. `ncp-agent` 必须作为 root 原生服务运行，并独占 Docker Socket、systemd、journald、PTY 与宿主机能力访问。
2. 浏览器只与 `ncp-server` 通信；Server 将 Root 用户请求转为 Agent RPC。这是进程边界，不是对 Root 用户功能的降级。
3. Agent 以覆盖 Docker、系统、日志、终端、文件和 Compose 的**强类型 RPC**提供能力；宿主机 Root 终端通过专用 PTY 会话提供，不以字符串形式伪装成通用 HTTP `exec` 接口。
4. 业务模块不得绕过 Agent 访问宿主机；Docker 实时状态以 Docker Engine 为事实来源，不把完整实时数据复制到 SQLite。
5. 耗时操作通过 Job 执行，避免 HTTP 请求超时；数据库迁移必须可重复执行并通过测试；API 变更必须同步更新 OpenAPI。

## 编码与安全约束

- Go 代码必须通过 `go test ./...`；新增行为遵循先写失败测试、再最小实现的 TDD 流程。
- 不拼接用户输入形成 Shell 命令；受控命令必须使用参数数组，并设置超时。Root 终端使用独立 PTY 会话。
- 不记录或返回密码、Token、Cookie、私钥、连接串或其他敏感值。
- API 使用稳定错误码，调用方不得依赖错误文本判断行为。
- 流式调用必须响应 Context 取消；Docker Event 断线后必须触发对账。
- Root 角色的管理操作以 Job 结果和真实运行状态为准；终端 Session 必须支持关闭、断线和资源回收。

## 工作方式

每个功能按可审查范围推进：ADR → 接口定义 → 数据模型/迁移（若需要）→ Agent RPC → 后端实现 → 单元测试 → 前端 → 集成测试 → 文档。文档、注释和 Conventional Commit 描述默认使用中文，类型 token 使用英文，例如 `feat(system)：实现环境能力探测器`。
