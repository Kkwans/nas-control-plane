## ADDED Requirements

### Requirement: 项目详情抽屉
服务入口和 Docker 管理 MUST 能打开同一项目详情抽屉，展示项目状态、类型、目录、容器统计、公开端口与全部容器。

#### Scenario: 用户查看项目详情
- **WHEN** 用户点击项目名称、详情按钮或卡片主体
- **THEN** 页面打开项目详情并保留原列表滚动位置

### Requirement: 可恢复详情地址
打开的项目详情 MUST 写入当前页面 URL 查询参数并支持浏览器前进、后退和刷新恢复。

#### Scenario: 用户刷新详情页面
- **WHEN** URL 含有有效 `project` 查询参数且实时清单已加载
- **THEN** 页面自动恢复对应项目详情

### Requirement: 详情内真实操作
Docker 管理页的详情 MUST 复用现有容器生命周期和日志能力，服务入口页则 MUST 只提供安全的查看与端口访问操作。

#### Scenario: 用户从 Docker 详情查看日志
- **WHEN** 用户点击某个容器的“查看日志”
- **THEN** 页面打开该容器日志且项目详情上下文保持不变
