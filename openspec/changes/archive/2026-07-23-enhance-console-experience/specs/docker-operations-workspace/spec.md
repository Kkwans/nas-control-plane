## MODIFIED Requirements

### Requirement: Docker 项目总表
Docker 管理页面 MUST 在桌面以高密度表格、在手机以项目卡片展示 Compose 项目和独立容器组，并提供状态、容器数量、公开端口、工作目录和详情操作。

#### Scenario: Docker 清单已加载
- **WHEN** Root Agent 返回一个或多个项目
- **THEN** 用户无需浏览大型空白卡片即可比较多个项目的状态、端口和容器数量

#### Scenario: 手机查看 Docker 清单
- **WHEN** 视口宽度小于 768px
- **THEN** 页面隐藏桌面表头并以无横向滚动的项目卡片展示相同核心信息

### Requirement: 项目容器详情
每个项目 MUST 能通过项目行或详情按钮打开详情工作区，并展示容器名、镜像、运行状态、端口、创建时间和生命周期操作。

#### Scenario: 用户打开项目详情
- **WHEN** 用户点击项目行、项目名称或详情按钮
- **THEN** 页面在详情抽屉展示属于该项目的全部容器，而不是只显示前三个

#### Scenario: 用户通过详情链接刷新页面
- **WHEN** URL 中包含有效项目标识
- **THEN** Docker 清单加载后恢复对应项目详情

### Requirement: 容器日志工作区
用户 MUST 能从项目详情中的容器项打开日志抽屉，抽屉显示容器名称、日志行数、stdout/stderr 和关闭操作。

#### Scenario: 用户查看容器日志
- **WHEN** 日志请求成功
- **THEN** 日志抽屉显示日志尾部，关闭后仍返回原项目详情

### Requirement: 服务端口直达
已公开的容器端口 MUST 在服务入口、Docker 项目表和项目详情中显示为可访问链接。

#### Scenario: 项目存在公开端口
- **WHEN** Docker 清单包含大于零的 public port
- **THEN** 页面显示端口号、协议和打开新标签页的中文 Tooltip

#### Scenario: 项目没有公开端口
- **WHEN** 项目所有容器均无 public port
- **THEN** 页面明确显示“无公开端口”，不留下空白操作区
