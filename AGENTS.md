# NAS Control Plane（NCP）项目约束

## 当前阶段

当前只实现和验证 Phase 0 技术验证能力。不得跳过 Phase 0 直接实现完整管理面板，也不得在未获得当次明确授权时部署、启动、停止或重启 NAS 服务、Docker 容器、Compose 项目或 Caddy。

## 架构约束

1. Web Server 不得直接访问 Docker Socket。
2. Server 不得以 root 身份运行。
3. Agent 不得暴露任意命令执行 RPC。
4. 业务模块不得绕过 Agent 访问宿主机。
5. Docker 实时状态以 Docker Engine 为事实来源，不把完整实时数据复制到 SQLite。
6. 长任务必须通过 Job 系统执行；危险操作必须生成 Audit Event。
7. 数据库迁移必须可重复执行并通过测试；API 变更必须同步更新 OpenAPI。

## 编码与安全约束

- Go 代码必须通过 `go test ./...`；新增行为遵循先写失败测试、再最小实现的 TDD 流程。
- 不拼接用户输入形成 Shell 命令；受控命令必须使用参数数组，并设置超时。
- 不记录或返回密码、Token、Cookie、私钥、连接串或其他敏感值。
- API 使用稳定错误码，调用方不得依赖错误文本判断行为。
- 流式调用必须响应 Context 取消；Docker Event 断线后必须触发对账。
- 删除操作不得批量静默执行；数据库恢复不得覆盖未经确认的目标；终端 Session 必须限制资源。

## 工作方式

每个功能按可审查范围推进：ADR → 接口定义 → 数据模型/迁移（若需要）→ Agent RPC → 后端实现 → 单元测试 → 前端 → 集成测试 → 文档。文档、注释和 Conventional Commit 描述默认使用中文，类型 token 使用英文，例如 `feat(system)：实现环境能力探测器`。
