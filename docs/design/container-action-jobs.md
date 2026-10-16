# 单容器生命周期持久 Job

## 阶段与边界

阶段1仅交付兼容后端入口，前端尚未切换，不代表端到端产品已完成。旧同步接口保留。阶段2再处理前端确认、接收锁、丢回执只读核对与 Job 展示；当前不做批量、取消、重试或自动重放。

## 固定合同

- 新 POST `/api/v1/docker/containers/{containerID}/action-jobs/{action}`，沿用 RootSession 与 origin 防护，无请求体。只接受64位小写十六进制完整 ID 和精确小写 start/stop/restart；不接受名字或短 ID。
- 类型为 docker-container-start/stop/restart，reference 仅保存完整 ID。请求结构按值冻结在闭包，无新 schema、payload 或 Agent 协议。
- 持久创建后才返回202原始 queued 快照：cancellable=false、artifactState=unknown、所有进度数值0、layers={}，无终态/取消/保存错误字段。接收不等于执行完成。
- 三种动作与镜像删除共享 removals 单 worker 队列，不构成对其他入口的全局互斥。pull/compose/removals 仍3/1/1，总容量32，排队期限10分钟。
- engine context 加既有45秒 API预算，Agent现有30秒不变；HTTP断开不取消。running持久化成功才派发。每个任务最多一次 ControlContainer，保存失败只重存结果。
- 严格核对回执完整 ID、精确动作、非空名称与 running/stopped 状态。start/restart读回running、stop读回stopped才completed。完成仅证明本次调用和读回，不保证健康或持续状态。
- 归属有效但明确反态：failed / DOCKER_CONTAINER_STATE_MISMATCH，文案明确命令已返回但读回未达目标。缺字段、错误归属、未知状态及 Agent 解码回执无效：interrupted / DOCKER_CONTAINER_ACTION_RECEIPT_INVALID。
- 超时：interrupted / JOB_EXECUTION_TIMEOUT；取消、传输等不确定结果：interrupted / JOB_EXECUTION_INTERRUPTED。NOT_FOUND可能来自后置inspect，ACTION_FAILED/INSPECT_FAILED也不证明未执行。
- 仅固定白名单 DOCKER_CONTAINER_ACTION_INVALID、AGENT_PROTOCOL_MISMATCH、AGENT_RPC_REQUEST_INVALID 且context正常时可以failed并说明派发前拒绝。
- 恢复只读取历史并把未结束任务标为interrupted，不重放；所有新类型恢复后仍不可取消，retry被拒绝。历史保留完整动作类型和ID。

## 实施与验证

新增严格入口/runner/纯判定函数；仅扩展engine类型白名单与removals队列选择；不改变旧同步处理器与存储schema。OpenAPI同步新增合同与旧路由共存。

验证限定 Recorder、fakeAgent、内存store和临时SQLite：严格参数/路由保护；持久前零调用；202初始快照；HTTP取消独立执行；回执归属与反态；错误分类；create/ACK/running/terminal保存边界且单次调用；共享队列串行与TTL；重启不重放及cancel/retry拒绝。不启动监听器、socket、PTY、helper、浏览器或Docker，不访问NAS或用户电脑。

## 阶段2：前端接收保护与只读核对

单容器列表、移动端列表与项目详情的 start/stop/restart 统一使用新入口，不回退同步 API。stop/restart 保留原确认框、键盘取消焦点及所有者防护；start 不额外确认。确认前冻结完整 ID、动作、展示名称并登记登录会话级占用，同 ID 三种动作全部受保护。取消且未派发时只释放自己的占用；派发后清单刷新、容器消失、路由卸载都不释放，晚202仍写入原会话，旧视图不提示。会话失效清空原会话占用。

202严格核对初始字段、类型和完整reference，只提示已接收。重定向、非规范响应、网络错误均视为接收未知；只有精确 HTTP 状态及稳定错误码白名单证明未接收。新入口、旧同步API与项目/Compose批量API并存，但UI单容器不使用旧入口。项目批量入口增加当前清单受保护容器的本地guard，不承诺覆盖外部或清单不可见目标的互斥。

独立集中面板不依赖容器是否仍存在，显示完整容器ID、动作、已知Job ID和保护原因。接收后、重新进入页面以及手动数据刷新时进行一次精确Job GET核对，之后用户也可在面板手动只读核对；不增加自动轮询、取消、重试或重放。已知Job仅精确ID/type/reference核对，queued/running/结果保存中/404/读取失败保留保护。只有严格读回completed可自动释放；failed/interrupted/状态不符须只读刷新成功及明确本地风险确认，解除动作不发POST。风险确认使用token、会话和核对版本，过期确认不能释放新保护。

丢失Job ID时只读查询最近任务与清单，绝不按目标或时间认领任务。若观察到同完整ID活动动作Job，禁止解除；读取失败不能清除已经观察到的活动证据。未知接收可在只读核对尝试后明确放弃本页防重复保护，文案说明核对失败或当前容器状态都不是未接收/未执行证明。提交尚在等待响应时不可解除。

保护只覆盖当前登录会话的跨路由/清单刷新，不使用localStorage，不保证整页重载、多标签页或其他客户端幂等；不新增服务端幂等。任务中心与已保存历史增加三种明确动作名称；历史不作为当前完成或解锁依据。

阶段2验证限定现有依赖的纯Vitest/jsdom、ESLint、vue-tsc和生产构建。浏览器键盘/布局实证在后续独立CI片验证，本阶段不启动浏览器、监听器、socket、PTY、Docker或访问NAS。
