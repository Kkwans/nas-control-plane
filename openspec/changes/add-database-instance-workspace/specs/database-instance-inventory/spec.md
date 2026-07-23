## ADDED Requirements

### Requirement: 基于 Docker 事实发现数据库实例
系统 MUST 从 Root Agent 返回的 Docker 容器清单识别 MySQL、MariaDB、PostgreSQL、Redis 和 Valkey 实例，不得通过扫描端口或读取容器秘密信息猜测实例。

#### Scenario: 存在受支持的数据库镜像
- **WHEN** Docker 清单包含明确匹配受支持数据库引擎的镜像
- **THEN** 数据库实例 API 返回对应实例、引擎类型和容器状态

#### Scenario: 容器不是受支持的数据库
- **WHEN** 镜像、服务名和容器名均不匹配受支持引擎
- **THEN** 系统不把该容器加入数据库实例清单

### Requirement: 返回只读实例元数据
数据库实例 API MUST 返回采集时间、稳定容器标识、名称、引擎、镜像、状态、项目、服务、端口、创建时间和挂载摘要，且 MUST NOT 返回环境变量、密码、连接串或数据内容。

#### Scenario: Root 用户读取实例清单
- **WHEN** Root 会话请求 `/api/v1/databases/instances`
- **THEN** Server 返回实例元数据和类型汇总，不连接数据库服务

#### Scenario: 请求没有有效会话
- **WHEN** 未登录客户端请求数据库实例接口
- **THEN** Server 返回统一的未认证错误且不返回实例信息

### Requirement: 数据挂载可解释
实例挂载摘要 MUST 仅包含挂载类型、源、目标和读写状态，并优先标识常见数据库数据目录。

#### Scenario: 数据库容器具有数据挂载
- **WHEN** Docker 清单包含实例挂载
- **THEN** API 返回挂载摘要，前端可区分宿主机路径、命名卷和容器目标目录
