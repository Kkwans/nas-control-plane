# NAS Control Plane 阶段进度

更新时间：2026-07-23

## 当前结论

项目已从“继续扩展 Phase 0”切换为 **Root 控制面 MVP** 交付。现有 PoC 是后续真实功能的技术依据；不再把 P0-03 systemd 实机验证和 P0-06 Caddy 实机验证作为首页、Docker、服务、日志和终端功能的阻塞项。

## 已完成

| 范围 | 状态 | 证据与边界 |
| --- | --- | --- |
| P0-01 环境能力探测 | 已完成 | Go 单测、ARM64 交叉构建和 NAS 实机探测通过。 |
| P0-02 Docker SDK 探针 | 已完成 | 仅针对固定标签的临时容器完成实机验证，验证容器已清理。 |
| Root Agent 实时系统与 Docker 数据通道 | 代码完成，待实机部署 | 已实现系统快照、Docker Engine/容器/Compose 自动发现、Agent Dashboard gRPC 和 `/api/v1/system/summary`、`/api/v1/docker/inventory`、`/api/v1/services`；Go 全量测试、vet 与 Linux ARM64 交叉构建通过。尚待以 root systemd 服务部署到 NAS 后完成实机读取验证。 |
| P0-04 受控终端 PoC | 已完成 | PTY、WebSocket、尺寸同步、退出和资源上限均经 NAS 实机 PoC 验证；仅通过显式参数开启。 |
| P0-05 journald PoC | 已完成 | 查询、筛选、Cursor 分页和 Follow 流均经 NAS 实机 PoC 验证。 |
| P0-06 Caddy 路由持久化 PoC | 代码完成，运行时暂停 | 受限 Admin API 客户端和持久化编排已测试并提交；NAS 当前无运行中的 Caddy，运行时验证留待后续阶段。 |
| Vue 控制台 | 已完成基础界面，待接入真实 API | 已建立总览、服务中心、能力地图、响应式布局、统一间距/圆角/色彩/动效语言；下一切片切换为浅色现代视觉并接入新的实时系统与服务 API。 |

## 当前预览部署

当前 Compose 部署只承载前端预览。它没有接入 Root Agent 或真实 API，因此不能作为完整 NCP 发布版本。

- Compose 项目名：`nas-control-plane`
- 容器名：`nas-control-plane-console`
- 计划端口：`8760`
- 项目登记：使用标准 Docker Compose 项目元数据，由 `docker compose ls` 识别；不写入绿联未公开的内部数据库。
- API 行为：`/api/` 明确返回 Phase 0 尚未部署的状态，前端进入预览态，不伪装为真实系统数据。

### 已完成部署记录

- Compose 已启动 `nas-control-plane-console`，容器状态为 `running/healthy`。
- `docker compose ls` 已识别项目 `nas-control-plane`，配置文件为 `deploy/console/docker-compose.yml`。
- NAS 本机与局域网侧访问均已验证：根页面、`/services` SPA 路由和 `/healthz` 均返回 HTTP 200。
- 访问方式：使用 NAS 的局域网地址加端口 `8760`。该容器仅提供静态预览控制台；`/api/` 明确返回 Phase 0 API 待部署状态。

## Root 控制面 MVP 的交付顺序

1. 部署 Root `ncp-agent` systemd 服务和 Unix Socket，并将 Server 的 Agent 调用接到真实 NAS。
2. 实现 SQLite 初始化与登录：MVP 仅创建一个 `root` 角色，登录后拥有全部已实现的能力。
3. 交付真实 `/system`、`/docker`、`/services` 与 `/logs` API，并将总览、服务中心、能力地图替换为实时数据。
4. 交付容器启停重启、容器日志、Docker/Compose 项目发现、主机与容器终端，耗时操作进入 Job。
5. 再处理 Caddy、数据库备份恢复、多用户权限、历史指标和高级 Compose 编辑。

## 暂不阻塞 MVP 的事项

- Caddy 运行时路由持久化 PoC。
- 多用户细粒度 RBAC、WebAuthn 和面向公网的访问能力。
- 长期历史监控、数据库恢复与面板在线升级。
