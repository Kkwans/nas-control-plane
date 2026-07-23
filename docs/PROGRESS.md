# NAS Control Plane 阶段进度

更新时间：2026-07-23

## 当前结论

NCP 已完成 Root 控制面第一轮可用性重构。现有链路为“Root Agent → Unix Socket gRPC → Server HTTP API → Vue 控制台”，页面不再依赖静态模拟数据；登录后会立即加载真实主机与 Docker 数据，并通过 SSE 自动刷新，断线时降级为轮询。

## 本轮完成

| 模块 | 状态 | 说明 |
| --- | --- | --- |
| OpenSpec SDD 工作流 | 已完成 | 建立 `redesign-console-and-realtime` 变更，维护 proposal、design、差异规格与任务清单；项目规则切换为规格驱动。 |
| 控制台信息架构 | 已完成 | 当前可用导航统一为“总览、服务入口、Docker 管理、系统信息”；数据库、监控、日志中心、终端与系统设置明确标记为待接入。 |
| 中文化与高密度布局 | 已完成 | 移除巨型营销文案和装饰性英文，统一浅色后台设计语言、紧凑间距、表格与网格布局、40 像素操作按钮和中文 Tooltip。 |
| 实时数据 | 已完成 | 新增受认证保护的 `/api/v1/system/events` SSE 端点；首次进入自动加载，SSE 每 5 秒触发快照，断线后以 15 秒轮询降级。 |
| Docker 工作区 | 已完成 | 新增项目表格、项目容器展开详情、端口入口、启停/重启操作和日志抽屉。 |
| 服务入口 | 已完成 | 以紧凑三列网格展示 Compose 项目和公开端口，提供搜索、状态汇总和统一访问入口。 |
| 系统信息 | 已完成 | 以紧凑表格与信息卡展示系统、Root Agent、Docker Engine 和能力接入状态。 |

## 构建证据

按本轮约定不执行测试套件，仅完成交付必需的编译检查：

```text
go build -buildvcs=false ./cmd/ncp-agent ./cmd/ncp-server
pnpm run build
```

Windows 同步副本的 Go 编译、Vue 类型检查和生产构建均已通过。NAS ARM64 构建、镜像和部署状态以本轮 SSH 交付结果为准。

## 当前运行态

- Root Agent systemd 单元已启用并以 root 身份运行。
- Compose 项目已注册到 UGREEN Docker 项目列表，项目名为 `nas-control-plane`。
- 当前运行版本为 `nas-control-plane:2026.7.23-v3`（ARM64）；Server 与 Console 均为 healthy，控制台 `/healthz` 返回 200。
- `/api/v1/system/events` 已通过 Nginx 到达 Server；未登录请求返回 401，表明实时端点已启用且保持认证保护。
- UGREEN Docker 项目记录仍为运行状态，Compose 路径和两个容器的关联数量正确。
- 旧镜像保留，用于必要时回滚。

## 后续路线

1. 数据库管理：实例发现、连接配置、数据库与备份管理。
2. 监控：历史 CPU、内存、磁盘、网络曲线与告警数据。
3. 日志中心：系统日志、NCP 日志、容器日志统一检索。
4. 终端：主机和容器终端入口。
5. 系统设置：刷新频率、服务入口别名与面板配置。
