# NAS Control Plane 阶段进度

更新时间：2026-07-28

## 当前结论

NCP 已完成从“技术验证页面”到可持续开发的 NAS Root 管理控制台的主体切换。浏览器只访问 NCP Server；Server 使用 Unix Socket 强类型 RPC 调用以 root 身份运行的 Agent。当前阶段不再使用 OpenSpec 作为默认流程，需求、设计决策和验收证据由执行计划、OpenAPI、README 和本文件维护。

## 本阶段完成

| 模块 | 状态 | 关键结果 |
| --- | --- | --- |
| Agent / Server 契约 | 已完成 | 增加协议与构建版本，容器控制返回可诊断错误；Agent 与 Server 同提交构建 |
| 设置与列表偏好 | 已完成 | 自动保存、失败回滚；字号、密度、字体真实生效；列表分页和排序独立持久化 |
| 站点中心 | 已完成 | Web 内容探测、Favicon、手动 CRUD、图标上传事务回滚、收藏排序、忽略与恢复 |
| Docker 项目与容器 | 已完成 | 统一资源表格、容器控制、详情、日志和 Compose 工作台 |
| Compose | 已完成 | CodeMirror YAML 高亮、Tab 缩进、草稿、校验、部署任务和版本记录 |
| Docker Hub | 已完成 | 关键字搜索、分页排序、仓库元数据、标签选择和架构信息 |
| 镜像任务 | 已完成 | SQLite 持久化、最多 3 个并发任务、真实分层进度、速度、历史、重试和记录删除 |
| 日志中心 | 已完成 | 真实时间戳与时间范围、稳定事件 ID、增量 SSE、延迟骨架、分页和居中详情弹窗 |
| 终端 | 已完成 | Root/容器 PTY、浅色 xterm、条形光标、ANSI 配色和可选 ble.sh |
| 数据库 | 已完成 | 自动发现、项目分组与归档、数据表信息、数据 CRUD 和 SQL 工作台 |
| 系统信息 | 已完成 | 设备摘要、硬件系统、控制链路和能力检测四层信息架构 |
| 用户管理 | 已完成 | 等权 Root 账号新增、启停、删除、改密、可配置密码规则、会话撤销与最后账号保护 |

## 验证状态

分模块已完成的聚焦验证：

```text
go test ./internal/auth ./internal/httpapi
go test ./internal/docker ./internal/agentsocket ./internal/controlstore ./internal/httpapi
pnpm run typecheck
```

最终统一验证和 NAS 部署尚在进行，完成后补充：

- `go test ./...`
- Linux ARM64 Agent / Server 构建
- Vue 类型检查与生产构建
- systemd、Compose 健康和 API 契约
- 1440px、1024px、390px 浏览器综合验收

## 后续范围

本阶段不包含 RBAC、私有 Registry 凭据管理、终端多会话管理、日志导出和高级告警。用户账号当前均为等权 Root，这与 NCP 的局域网 Root 控制台定位一致。
