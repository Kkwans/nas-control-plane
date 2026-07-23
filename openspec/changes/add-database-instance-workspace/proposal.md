## Why

NCP 已能管理 Docker 项目，但数据库仍只是不可进入的占位菜单，用户无法统一查看 NAS 上实际运行的 MySQL、MariaDB、PostgreSQL、Redis 或 Valkey 实例。与此同时，已部署首页在真实登录验收中仍可能停留在“等待数据”，必须先保证首次快照和持续采样可靠更新。

> 范围说明：本 OpenSpec 变更仅保留为数据库阶段 A 的实例发现与工作区记录。数据库管理的完整目标、适配器架构、SQL/Redis 执行、可视化 CRUD、结构管理和审计以 `docs/specs/database-management-architecture.md` 为准；后续局部切片不再强制重复完整 OpenSpec 流程。

## What Changes

- 从 Root Agent 已采集的 Docker 容器清单识别数据库实例，不读取容器环境变量、密码、连接串或数据库内容。
- 新增受 Root 会话保护的数据库实例 API，返回类型、镜像、运行状态、项目、端口和数据挂载等只读信息。
- 将“数据库”接入主导航，新增高密度数据库工作区、类型与状态筛选、实例详情和移动端布局。
- 修复登录后首次数据不自动加载以及 SSE 已连接但快照不持续更新时缺少可靠兜底的问题。
- 同步更新 OpenAPI、设计系统说明和阶段进度。

## Capabilities

### New Capabilities

- `database-instance-inventory`: 定义基于 Docker 事实源发现并安全返回数据库实例元数据的行为。
- `database-management-workspace`: 定义数据库实例列表、筛选、详情、空状态与响应式交互。

### Modified Capabilities

- `console-information-architecture`: 将数据库从待接入占位项升级为正式主导航模块。
- `realtime-dashboard`: 要求登录后立即加载首次快照，并在实时流无事件时通过周期刷新保证持续更新。

## Impact

- 后端新增数据库实例分类领域模型和 `/api/v1/databases/instances` 接口，并扩展 Docker 清单中的挂载元数据。
- 前端新增数据库 API 类型、Pinia 数据状态、`/databases` 路由和数据库工作区页面。
- 实时刷新逻辑增加立即刷新和周期兜底，不改变现有 SSE 接口。
- 不新增数据库驱动，不连接数据库服务，不修改数据库账号、数据卷或运行中实例。
