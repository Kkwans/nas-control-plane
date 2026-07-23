## ADDED Requirements

### Requirement: Docker 项目总表
Docker 管理页面 MUST 以高密度表格展示 Compose 项目和独立容器组，并提供状态、容器数量、公开端口、工作目录和操作列。

#### Scenario: Docker 清单已加载
- **WHEN** Root Agent 返回一个或多个项目
- **THEN** 用户无需滚动大型卡片即可比较多个项目的状态、端口和容器数量

### Requirement: 项目容器详情
每个项目 MUST 能展开其容器明细，并展示容器名、镜像、运行状态、端口和生命周期操作。

#### Scenario: 用户展开项目
- **WHEN** 用户点击项目行或展开按钮
- **THEN** 页面在相邻区域展示属于该项目的全部容器，而不是只显示前三个

### Requirement: 容器日志工作区
用户 MUST 能从容器行打开日志抽屉，抽屉显示容器名称、日志行数、stdout/stderr 和关闭操作。

#### Scenario: 用户查看容器日志
- **WHEN** 日志请求成功
- **THEN** 右侧抽屉显示日志尾部并保持 Docker 项目列表位置不变

### Requirement: 服务端口直达
已公开的容器端口 MUST 在服务入口和 Docker 项目表中显示为可访问链接。

#### Scenario: 项目存在公开端口
- **WHEN** Docker 清单包含大于零的 public port
- **THEN** 页面显示端口号和打开新标签页的中文 Tooltip

#### Scenario: 项目没有公开端口
- **WHEN** 项目所有容器均无 public port
- **THEN** 页面明确显示“无公开端口”，不留下空白操作区
