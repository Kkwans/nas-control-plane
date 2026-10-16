# 独立容器项目生命周期 Job

## 边界与合同

新增 `POST /api/v1/docker/projects/standalone/action-jobs/{start|stop|restart}`；只接受 `containerIds`（1–128 个不重复的 64 位小写十六进制完整 ID），拒绝未知、重复 JSON 字段及尾随数据，正文最多 16 KiB。冻结用户选择的子集，不重新按状态筛选、不添加容器。旧同步 API、Compose、Agent/RPC、权限、前端保持不变。

类型为 `docker-project-start/stop/restart`，reference 固定 `standalone`。复用 removals 单 worker、全局容量 32、排队期限 10 分钟，不新增 mutation 并行，不承诺与旧同步入口互斥。202 是原始 durable queued 快照。HTTP 取消不影响已接收任务；cancel/retry 不可用，重启不重放。

## 执行与安全结果

running 持久确认后，用独立 5 秒预算读取既有 inventory；冻结目标须均唯一出现且归属于 standalone。预检失败零 mutation，不依据容器状态重选。归属预检并非原子保护。随后仅一次既有 ControlStandaloneProject，沿用 45 秒 mutation 预算。此切片不解决批次耗时和 Agent worker 问题，大批次可能超时，不能据此断言未执行。

先验证完整回执：项目/kind/action、完整无重复集合、每项动作/状态/成功与白名单错误码、Completed 与 State 汇总必须一致。坏整体不保留任意细项。换序合法，投影恢复冻结顺序。

- 全成功：completed。
- 仅明确反态：failed / DOCKER_PROJECT_STATE_MISMATCH，明确操作已发生。
- 任一执行未知：interrupted；有效完整回执可保留已确认项，未知项统一 unknown/JOB_EXECUTION_INTERRUPTED。
- 传输、超时、取消、无效回执：interrupted，无细项；仅固定派发前拒绝白名单可 failed。

专用 `project_action_result_json` 保存 `projectId/kind/action/items`，每项仅 `containerId/outcome/state/errorCode`，outcome 为 succeeded/state-mismatch/unknown。不保存名称、原始错误、配置或完整请求。结果与终态单次 CAS，pending 不外露，ACK 比较包含结果，切片深复制。严格有界解析 64 KiB/128 项，重复迁移、SQL 有界读取、坏历史不可成功；历史摘要不读取或包含结果。

## 验证

本切片采用纯 fake、Recorder、临时 SQLite 与 race，覆盖严格入参、预检归属、整体验证与最小投影、持久门槛/结果 CAS、重启不重放、只重存、兼容路径与 OpenAPI。httpapi 使用定向测试；这些结果不代表真实 Docker 或 NAS 验收。

## 前端接入与会话边界

前端分为严格传输、会话注册表、互斥基础、结果面板与页面接线五个模块。接纳仅接受原始 queued 快照；详情绑定精确 Job、动作及完整冻结 ID 集合，坏回执整体拒绝。详情响应最多 256 KiB，三种 type 列表分别最多 8 MiB、1024 行；超限读取失败并保留保护，不通过放大内存预算寻找解除证据。列表只观察有效活动任务，不认领未知提交。

会话注册表仅保存冻结意图与深冻结安全证据。scope 卸载仅取消自己未派发的确认；迟到接纳归原会话，退出登录使旧会话失效。已知任务以精确 GET 核对，未知任务读取三种 type。failed/interrupted 或接纳未知须本轮任务读取成功、清单成功、无活动证据，并明确确认重复执行风险后才解除。活动、pending、404、损坏读取均不构成解除依据。完成回执仅消费一次，清单刷新失败不抹去可信结果；开始新意图时清除旧完成卡，避免误认为新操作已完成。

本登录会话内，standalone 项目生命周期操作与容器创建、单容器生命周期、项目删除、Compose 生命周期相互排斥；其他原有操作之间仍维持原并发关系。确认前同步取得令牌，派发时再次核对；Compose 保留同步接口，未派发确认随视图释放，已派发请求的保护保持至 Promise 结束。该保护不是服务端全局锁，不覆盖整页重载、其他标签页/客户端，也不覆盖 Compose 编辑器部署/回滚或镜像操作。后端冻结子集及只读归属预检仍不是原子归属锁。

页面在清单之外展示保护和安全逐项结果；接纳不提示成功，不提供重试/取消/自动重跑失败项。大范围使用原生 details 显示全部 ID 与结果，超过8项默认折叠，核对控件置前。stop/restart 确认展示完整冻结范围，正文有界滚动，start 保留现有直接提交策略。桌面、移动、详情和创建入口采用真实 disabled 与调用方 guard；手动刷新核对 known/unknown 项目任务并复用同次清单读取。

纯单元测试覆盖上述生命周期和跨模块竞争；新增隔离浏览器旅程另行核验。浏览器脚本收集成功、静态类型检查及 jsdom 通过不等于实际浏览器焦点、几何、无障碍或 NAS 验收。
