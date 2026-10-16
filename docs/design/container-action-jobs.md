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
