## MODIFIED Requirements

### Requirement: 首次进入自动加载
认证完成后，控制台 MUST 由实时数据控制器立即请求主机能力、主机快照和 Docker 清单，不得依赖登录页面组件继续存活，也不得要求用户手动点击刷新才能看到数据。

#### Scenario: Root 用户首次进入控制台
- **WHEN** 认证状态确认成功或登录成功
- **THEN** 实时数据控制器立即开始加载，并在接口返回后展示真实数据

#### Scenario: 首次请求与 SSE 初始事件重叠
- **WHEN** 首次刷新尚未结束时收到 `snapshot` 事件
- **THEN** 前端合并并发刷新且至少保留一份成功快照

### Requirement: 断线恢复与降级
实时连接 MUST 自动重连，并在登录期间以不高于 5 秒的周期刷新作为可靠兜底；同一时刻最多执行一轮快照刷新。

#### Scenario: SSE 暂时断开
- **WHEN** EventSource 进入错误状态
- **THEN** 页面显示“轮询更新”，并使用周期刷新继续获取真实数据

#### Scenario: SSE 已连接但没有可见事件
- **WHEN** EventSource 保持打开但 `snapshot` 事件没有驱动页面更新
- **THEN** 周期刷新仍持续更新主机、Docker 和数据库实例数据

#### Scenario: 页面重新可见
- **WHEN** 浏览器标签从隐藏恢复为可见
- **THEN** 前端立即执行一次刷新并恢复实时连接
