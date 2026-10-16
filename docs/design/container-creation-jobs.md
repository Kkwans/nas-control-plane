# 容器创建持久 Job（兼容后端阶段）

## 边界与合同

新增 POST `/api/v1/docker/containers/create-jobs`，沿用 RootSession、origin、控制正文大小/JSON 校验、Normalize 和 Agent 能力检查。旧 POST `/api/v1/docker/containers` 的同步201及 CreateContainerDrawer 保持原样；本阶段不声称前端已迁移。

输入在接纳前独立深拷贝所有 map、slice、pointer（包括 GPU options），仅由内存执行闭包持有。请求正文、名称、镜像、环境变量、命令等不写 Job reference、日志、文案或数据库；reference 为空。复用 entry.run 的资格授予后转移及未派发冻结/放弃清引用，不另加 payload holder。

创建专用单 worker、创建 queued/running/pending 总容量1，同时占用全局32。接纳在同一 admission 锁中检查两容量，失败回滚，所有释放只一次；pending 保存期间保留容量。pull/compose/removals 仍3/1/1，短动作不被创建占用同队列。排队 TTL10分钟；Server10分钟、Agent2分钟、cleanup15秒不变。

202仅在 queued 持久成功后返回原始接收快照。running 持久确认后才调用一次 Agent；HTTP取消不传播。完整回执先绑定规范化请求的 image、runContainer/started、created=true、完整64位小写十六进制ID及 running/stopped state，再投影。允许启动后退出，completed不保证持续running。所有实际 mutation RPC 派发后错误、超时、取消、无效回执保守 interrupted/未知；进入客户端方法不等于已派发：仅 context 正常时 DOCKER_CONTAINER_CREATE_INVALID、AGENT_PROTOCOL_MISMATCH、AGENT_RPC_REQUEST_INVALID 三个固定明确派发前拒绝码可 failed。START_FAILED/CLEANUP_FAILED 不自动补偿、重试或重放。

## 安全结果与持久化

仅 completed 的 docker-container-create 可含专用 createResult：containerId（完整ID）、created:true、started:boolean 三个必填字段。禁止 name/image/state/runContainer、未知/重复字段、额外JSON、超限输入。结果与 completed 在一次CAS中保存；queued/running/failed/interrupted及pending对外快照不得带成功结果。clone、pending与ACK全字段严格比较，保存失败只重存结果。

jobs新增 create_result_json 列，通过可重复 ensureColumn 迁移。异常历史不成为可用成功；历史摘要不增加结果，精确详情可读取安全结果。恢复 queued/running 为interrupted且不重放，完成结果可读；取消、重试仍拒绝。

## 实施与验证

- 专用入口、深拷贝与严格回执投影，保留旧入口。
- 最小扩展队列/容量与专用完成路径，迁移及严格有界结果存储。
- OpenAPI同步新增入口及专用结果schema。
- 仅 pure fake、Recorder、临时SQLite验证：深拷贝；秘密哨兵DB/API/SSE/历史/错误无泄漏；容量1/global32/TTL/close；running持久前零RPC；HTTP取消独立；pending/ACK只保存不重放；错误/残留未知；坏结果拒绝；重复旧库迁移；旧201；短动作独立。运行适用race、OpenAPI、build/vet与diff审查。不启监听器、socket、PTY、helper、浏览器、Docker，不访问NAS/用户电脑，不安装依赖。

## 本轮验证结果

后端兼容入口、专用队列与结果迁移已实现。新增 fake/Recorder/SQLite 测试覆盖上述边界，并补充同版本但结果不同的负向ACK、真实worker短动作独立执行、running持久化跨排队TTL及日志/SQLite文件秘密哨兵。新旧Job生命周期定向race、controlstore、OpenAPI验证通过；独立只读审查无阻塞发现。前端未切换，不代表生产部署或真实Docker验证。
