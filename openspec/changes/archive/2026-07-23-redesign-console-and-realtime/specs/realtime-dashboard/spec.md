## ADDED Requirements

### Requirement: 首次进入自动加载
认证完成后，控制台 MUST 立即请求主机能力、主机快照和 Docker 清单，不得要求用户手动点击刷新才能看到数据。

#### Scenario: Root 用户首次进入控制台
- **WHEN** 认证状态确认成功
- **THEN** 页面自动开始加载并在接口返回后展示真实数据

### Requirement: HTTP 长连接更新
Server MUST 提供受认证保护的 SSE 端点，前端 MUST 在登录期间保持连接并根据 `snapshot` 事件刷新权威 REST 快照。

#### Scenario: SSE 连接正常
- **WHEN** Server 每 5 秒发送一次 `snapshot` 事件
- **THEN** 前端刷新主机与 Docker 数据并更新最后同步时间，无需用户操作

#### Scenario: 用户退出登录
- **WHEN** Root 会话结束
- **THEN** 前端关闭 EventSource 和降级轮询，不再请求受保护数据

### Requirement: 断线恢复与降级
实时连接 MUST 自动重连，并在不可用时退回不高于 15 秒间隔的轮询；同一时刻最多执行一轮快照刷新。

#### Scenario: SSE 暂时断开
- **WHEN** EventSource 进入错误状态
- **THEN** 页面显示“正在重连”，并使用降级轮询继续刷新数据

#### Scenario: 页面重新可见
- **WHEN** 浏览器标签从隐藏恢复为可见
- **THEN** 前端立即执行一次刷新并恢复实时连接

### Requirement: 实时状态诚实表达
页面 MUST 区分正在连接、实时更新、降级轮询和不可用状态，不得在没有成功响应时显示“实时数据已同步”。

#### Scenario: 所有快照请求失败
- **WHEN** 当前刷新没有任何接口成功
- **THEN** 顶栏显示数据不可用并保留上一次成功快照，不伪造在线状态
