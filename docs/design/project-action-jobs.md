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
