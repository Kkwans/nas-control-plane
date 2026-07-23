## Why

当前控制台虽然已经接入真实数据，但服务入口与 Docker 管理仍缺乏清晰的页面层级、详情路径和操作反馈，首页也没有把持续采集的数据转化为可读趋势。桌面布局直接压缩到手机端，导致菜单、表格、工具区和触控目标在小屏幕上难以使用，因此需要进行一次以管理效率和跨设备体验为目标的系统性优化。

## What Changes

- 重构服务入口和 Docker 管理的顶部工作区，统一页面标题、实时摘要、搜索、筛选、视图切换和主要操作层级。
- 为 Compose 项目提供可深链的详情抽屉，展示基本信息、容器、端口、镜像、状态、目录和快捷操作。
- 将总览升级为实时趋势看板，展示 CPU、内存、负载与网络吞吐的短时历史图表，并保留可访问的文本摘要。
- 建立移动端导航规范：小屏幕使用顶部栏与汉堡菜单打开侧滑导航，详情使用全屏抽屉，表格转换为卡片而不是产生横向滚动。
- 统一字体层级、语义颜色、卡片边界、悬停/按压状态、Tooltip、焦点状态和 160–240ms 微动效。
- 不新增模拟数据；图表历史由当前会话收到的真实快照构成，重新进入页面后从当前时刻开始积累。

## Capabilities

### New Capabilities

- `console-workspace-layout`: 高密度管理页面的顶部工具区、摘要区、筛选与视图结构。
- `project-detail-experience`: 服务项目和 Docker 项目的详情查看、深链、容器信息与快捷操作。
- `realtime-trend-visualization`: 基于实时快照构建短时资源趋势、网络速率和图表交互。
- `responsive-console-navigation`: 桌面侧栏、移动端汉堡菜单、响应式内容与触控交互规范。

### Modified Capabilities

- `console-information-architecture`: 调整控制台页面层级与导航表现，使详情和移动端导航成为正式信息架构的一部分。
- `docker-operations-workspace`: 将项目列表从单层展开升级为列表与详情工作区，并为移动端提供卡片化操作布局。

## Impact

- 主要影响 `frontend/src/layout`、`frontend/src/views`、`frontend/src/stores`、共享组件和设计令牌。
- 复用现有 Vue 3、Element Plus、Lucide、ECharts 和 SSE 数据链路，不引入新的运行时依赖。
- 不改变 Root Agent、数据库结构和现有 Docker 控制 API；部署仍使用当前 NCP Compose 项目。
