# Compose 项目生命周期 Job

## 合同与边界

新增 `POST /api/v1/docker/compose/projects/{projectID}/action-jobs/{start|stop|restart}`，保留所有同步接口、Agent/RPC 与前端。仅接受 workingDirectory/configFiles，严格拒绝重复、未知、null、错类型、尾随和无效 UTF-8；正文64KiB、原始和规范化项目ID256字节、目录/文件路径4096字节、1–32文件，拒绝控制字符。沿用 LifecycleRequest.Normalize 的卷范围、项目内 YAML 路径、去重及顺序，复制冻结输入。

类型 compose-start/stop/restart，reference仅规范化项目ID。与部署共享 Compose 单worker，全局32容量、排队TTL10分钟；不是跨旧同步入口、其他类型、客户端的全局锁。202是原始 durable queued快照；HTTP取消不影响接收任务。running持久确认后以既有90秒预算仅一次ControlComposeProject；不重试、不取消、不重启重放、不回退同步。start是compose start而非up；projectID是关联元数据，实际目标由目录及文件决定，不是CLI -p参数。没有库存预检或原子配置归属保证。

## 完成证据与不确定性

完整回执须先验证项目/动作关联、Completed=true、1–1024项、身份UTF-8/控制字符/4096字节/规范空白、非空名称或ID、非空ID不重复；同名副本合法。不要求64位ID。Running须与canonical state一致。start/restart全running，stop全stopped/exited/created/dead，整体State须与目标一致。任何坏项丢弃整个回执。

仅completed保存composeActionResult：projectId/action/state/serviceCount。没有逐服务结果，不保存名称、容器ID、路径、配置、输出或原始错误。现有Agent错误路径不返回结果，因此不构造部分证据。命令/验证/能力不可用、传输失败或未知错误均interrupted；超时JOB_EXECUTION_TIMEOUT，坏回执COMPOSE_LIFECYCLE_RECEIPT_INVALID，其余JOB_EXECUTION_INTERRUPTED。只有context仍有效的COMPOSE_LIFECYCLE_INVALID、AGENT_PROTOCOL_MISMATCH、AGENT_RPC_REQUEST_INVALID可failed。排队到期仍failed/JOB_QUEUE_TIMEOUT且零执行。

成功只证明命令返回和一次观察，不保证健康、持续状态、配置服务完整性或重启次数。typed receipt不能区分缺失false与显式false；本片不改变已有Agent CombinedOutput/RPC解码内存边界。

## 持久化与恢复

专用compose_action_result_json与终态单次CAS；pending不外露成功或结果，重试仅重存。ACK比较包含结果，快照复制聚合结构。重复迁移与严格4096字节解析（涵盖256字节ID的JSON转义展开）拒绝错类型、重复/未知/缺失字段、尾随和跨结果污染；count为1–1024整数，类型/动作/状态/reference匹配，completed缺失结果也损坏。SQL读前按字节限制并替换损坏值；历史摘要不加载或包含结果。CAS依据已存不可变元数据验证。恢复queued/running为interrupted并清结果，不重放。沿用pending占额、关闭预算及已删除任务不可复活保护。

## 验证范围

仅fake Agent、Recorder和临时SQLite；定向httpapi测试不运行真实socket/listener/PTY/Docker/NAS。覆盖严格输入、规范化冻结、全回执/错误分类、durable门槛、单派发/仅重存、共享部署队列、快照/ACK、容量/TTL、恢复与损坏历史以及OpenAPI。隔离测试不等于真实NAS验收。
