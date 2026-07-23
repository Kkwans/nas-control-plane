# NCP 数据库管理架构与实施规划

更新日期：2026-07-23

## 1. 结论

数据库管理是 NCP 的核心模块，最终目标不是简单的实例发现或只读浏览，而是覆盖连接管理、元数据浏览、SQL/Redis 控制台、可视化数据编辑、结构管理、自动发现和审计的完整管理工作区。

实现采用“统一控制面 + 分数据库适配器”架构。关系型数据库共享连接、元数据、查询结果和表格编辑协议；Redis 使用独立的 Key/数据类型管理协议，不强行伪装成表格数据库。

## 2. 当前覆盖情况

| 能力 | 当前状态 | 缺口 |
| --- | --- | --- |
| NCP 自身存储 | 已有 | 仅使用 SQLite 保存 Root 用户和会话，没有数据库连接注册表 |
| 身份认证 | 已有 | 只有单一 Root 角色，没有数据库操作级审计和确认票据 |
| Docker 发现 | 部分具备 | 能看到通用容器、镜像、端口和 Compose 项目，但没有数据库分类和挂载摘要 |
| NAS 实机数据库 | 可观察 | 当前 Docker 元数据可识别 MySQL 8 和 Valkey，尚未纳入数据库模块 |
| 数据库连接 | 未实现 | 没有创建、编辑、测试、删除和加密保存能力 |
| 元数据浏览 | 未实现 | 没有数据库、Schema、表、视图、字段、索引和约束 API |
| SQL 执行 | 未实现 | 没有 SQL 编辑器、结果集、影响行数、耗时、事务和取消能力 |
| 表数据管理 | 未实现 | 没有分页、筛选、排序、搜索和行级增删改查 |
| 表结构管理 | 未实现 | 没有字段、主键、索引、约束和 DDL 管理 |
| Redis 管理 | 未实现 | 没有 SCAN、Key 浏览、类型编辑、TTL 和命令控制台 |
| 自动发现 | 未实现 | 没有主机服务、项目配置和 SQLite 文件发现 |
| 凭据保护 | 未实现 | 当前 SQLite 只保存密码哈希和会话哈希，没有可逆加密设施 |
| 操作审计 | 未实现 | 没有数据库操作审计表和查询界面 |

## 3. 总体架构

```text
Vue 数据库工作区
  ├─ 连接与发现
  ├─ 元数据树
  ├─ SQL 编辑器 / Redis 命令台
  ├─ 结果表格与行编辑
  └─ 结构管理与审计
          │ HTTPS / Root Session
          ▼
ncp-server
  ├─ Database Application Service
  ├─ Connection Registry
  ├─ Credential Vault
  ├─ Confirmation / Job / Audit
  └─ API DTO 与结果限制
          │ 强类型 Unix Socket RPC
          ▼
ncp-agent（root）
  ├─ Discovery Engine
  ├─ Relational Adapter
  │   ├─ MySQL / MariaDB
  │   ├─ PostgreSQL
  │   └─ SQLite
  └─ Redis Adapter
```

### 3.1 Server 职责

- 保存连接定义、发现候选、加密凭据、审计记录和操作确认状态。
- 校验 Root 会话、请求限制、超时、并发和结果行数。
- 将浏览器请求转换为强类型 Agent RPC。
- 生成 Job，记录执行人、目标实例、操作类型、耗时、结果和错误码。
- 不直接读取 NAS 宿主机文件或 Docker Socket。

### 3.2 Agent 职责

- 发现 Docker/Compose 数据库、宿主机服务、项目配置线索和 SQLite 文件。
- 使用数据库适配器建立短生命周期连接并执行受控请求。
- 访问 NAS 本地 SQLite 文件，执行元数据、查询和写入操作。
- 处理 Context 取消、连接超时、事务回滚、结果上限和资源释放。
- 不持久化数据库密码，不记录 SQL 参数中的敏感值。

## 4. 适配器设计

### 4.1 关系型数据库接口

业务层面定义能力接口，不直接暴露驱动差异：

- `TestConnection`
- `ListCatalogs` / `ListSchemas`
- `ListTables` / `DescribeTable`
- `ListIndexes` / `ListConstraints`
- `Query`
- `Execute`
- `BeginTransaction` / `Commit` / `Rollback`
- `ListRows`
- `InsertRow` / `UpdateRow` / `DeleteRow`
- `CreateObject` / `AlterObject` / `DropObject`

适配器内部负责标识符引用、分页语法、系统目录、类型映射、时区、字符集和错误码转换。

### 4.2 MySQL 与 MariaDB

- 共享大部分 `database/sql` 连接与信息架构实现。
- 单独维护版本能力矩阵，处理 JSON、生成列、字符集、索引和 DDL 差异。
- 分页使用 `LIMIT/OFFSET`；所有筛选值参数化，列名只能来自已加载元数据。

### 4.3 PostgreSQL

- 使用 PostgreSQL 专用驱动和 `pg_catalog`/`information_schema`。
- 明确区分 database、schema、table 和 view。
- 处理数组、JSONB、枚举、序列、identity、大小写标识符和 `RETURNING`。

### 4.4 SQLite

- 复用项目已有的纯 Go SQLite 能力，但管理连接与 NCP 自身认证库必须隔离。
- 通过 Root Agent 打开用户确认的本地文件。
- 使用 `sqlite_master`、PRAGMA 和事务实现元数据与结构操作。
- 系统数据库和正在使用的项目数据库默认标记为高风险目标。

### 4.5 Redis 与 Valkey

Redis/Valkey 使用独立接口：

- `ScanKeys`，只允许渐进式 SCAN，不使用阻塞式 `KEYS *`。
- `GetKey` / `SetKey` / `DeleteKey`
- String、Hash、List、Set、Sorted Set、Stream 分类型读取和编辑。
- `GetTTL` / `SetTTL` / `Persist`
- `ExecuteCommand`

命令控制台默认拒绝或要求二次确认 `FLUSHALL`、`FLUSHDB`、`SHUTDOWN`、`DEBUG`、危险 `CONFIG` 等操作。

## 5. 可视化表数据管理

### 5.1 查询与分页

- 默认每页 50 行，可选 20/50/100，服务端设置硬上限。
- 排序字段必须来自表元数据白名单，不能直接拼接用户文本。
- 筛选器使用结构化表达式，值全部参数化。
- 自定义 SQL 结果设置最大行数、最大字节数、执行超时和并发上限。
- 大结果集后续使用游标或流式分块，不一次加载到浏览器。

### 5.2 行级增删改查

- 有主键或唯一键时才默认开放可视化编辑。
- 更新和删除使用主键定位，并携带原始版本/原始值进行并发冲突检查。
- 没有稳定行标识的表默认只读，不使用模糊匹配删除。
- 新增、编辑使用数据库类型感知的表单；NULL、默认值、二进制和时间类型明确区分。

### 5.3 SQL 编辑器

- 支持多标签、执行选中语句、执行计划、结果集、影响行数、耗时和错误定位。
- SELECT 与写 SQL 都真正执行；写操作根据类型进入确认流程。
- 默认自动提交；显式事务提供开始、提交和回滚操作，并设置空闲超时。
- 每个执行请求有查询 ID，可取消，断开页面时释放事务和连接。

## 6. 自动发现

### 6.1 Docker 与 Compose

- 读取镜像、容器名、Compose service/project、端口、网络和挂载。
- 识别 MySQL、MariaDB、PostgreSQL、Redis、Valkey 等明确镜像。
- 可以读取 Compose 和项目配置的连接端点线索，但不把密码值返回页面或写入日志。
- 发现结果只形成候选连接，必须由用户确认后保存。

### 6.2 宿主机服务与监听端口

- Root Agent 结合监听端口、进程、systemd 单元和可执行文件识别数据库服务。
- 端口只作为线索，不因 3306/5432/6379 端口存在就直接认定数据库。
- 扫描必须有超时、并发和范围限制。

### 6.3 项目数据库

- 默认扫描受控根目录，例如项目目录和 Docker 项目目录。
- 识别 Compose、`.env`、Spring、Node、Python 等常见配置中的数据库类型、主机和数据库名。
- 凭据值只在受控解析过程短暂存在，不自动导入连接注册表。

### 6.4 SQLite 文件

- 使用文件头 `SQLite format 3` 验证，不只依赖扩展名。
- 采用限定根目录、深度、文件数量、大小和时间预算的增量扫描。
- 排除备份、缓存、构建产物和无关大目录。
- 根据路径、所有者、systemd/Compose 关联和项目归属标记“系统数据库”或“项目数据库”。

## 7. 凭据、安全与审计

### 7.1 凭据存储

- 连接元数据存入 NCP SQLite；密码使用 AES-256-GCM 等认证加密按记录加密。
- 主密钥保存在 NAS 独立的 root 管理秘密文件中，通过只读挂载提供给 Server，不写入 Git、同步目录或数据库。
- API 永远不返回原始密码；编辑连接时只能覆盖，不能读取回显。

### 7.2 操作保护

- 删除行、批量更新、DROP、TRUNCATE、清库、删除 Key 等操作采用“预检 → 风险摘要 → 短时确认票据 → 执行”两步协议。
- 系统数据库增加明显标识和更高确认等级。
- SQL/Redis 执行设置超时、结果限制、并发限制、事务空闲超时和取消能力。
- 不允许 Shell 拼接；SQL 标识符按方言引用，数据值参数化。

### 7.3 审计

审计记录包含用户、连接、目标对象、操作类型、语句指纹、开始/结束时间、影响行数、结果和错误码。默认不保存明文密码、连接串、完整敏感参数或完整结果集。

## 8. 前后端模块

### 后端

- `internal/database/registry`：连接与发现候选。
- `internal/database/crypto`：凭据加解密和密钥版本。
- `internal/database/audit`：操作审计。
- `internal/database/adapters/relational`：关系型统一接口。
- `internal/database/adapters/mysql`、`postgres`、`sqlite`。
- `internal/database/adapters/redis`：Key 与命令接口。
- Agent database RPC：发现、元数据、查询、数据修改、结构操作和取消。
- Server database API：连接、发现、目录、查询、表数据、结构、Redis 和审计。

### 前端

- 数据库总览与连接列表。
- 自动发现候选确认页。
- 关系型对象树与表结构页。
- SQL 编辑器和多结果集面板。
- 可编辑数据表格与结构化筛选器。
- Redis Key 浏览器、类型编辑器、TTL 和命令控制台。
- 审计日志与危险操作确认对话框。

## 9. 实施顺序

### 阶段 A：基础设施与自动发现

- 修复首页首次快照和持续采样。
- 建立连接注册表、凭据加密、审计表和适配器接口。
- Docker/Compose、主机服务、项目配置和 SQLite 文件发现。
- 覆盖 MySQL、MariaDB、PostgreSQL、SQLite、Redis/Valkey 候选识别。

### 阶段 B：连接与元数据

- 创建、编辑、测试、删除连接。
- 关系型数据库、Schema、表、视图、字段、索引和约束浏览。
- Redis 连接、实例信息和渐进式 Key 浏览。

### 阶段 C：查询与命令执行

- SQL 编辑器、结果集、影响行数、耗时、超时、取消和事务。
- Redis 类型读取、编辑、TTL 和命令控制台。
- 危险 SQL/命令二次确认和审计。

### 阶段 D：可视化数据 CRUD

- 分页、筛选、搜索、字段排序。
- 基于主键/唯一键的新增、编辑和删除。
- 并发冲突、NULL、默认值和类型感知编辑。

### 阶段 E：结构与生命周期

- 字段、主键、索引、约束和数据库对象 DDL。
- 系统数据库强化警告。
- 备份、恢复、导入、导出和定时任务另行规划。

## 10. 工作方式

本模块使用一份长期架构文档和阶段任务清单作为主要依据。明确的局部实现直接按项目规则开发；只有凭据迁移、连接注册表重大变更、跨进程 RPC 重构、备份恢复等高风险决策才按需使用 OpenSpec 或 ADR。
