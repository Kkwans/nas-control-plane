# 容器创建持久 Job

## 边界与合同

新增 POST `/api/v1/docker/containers/create-jobs`，沿用 RootSession、origin、控制正文大小/JSON 校验、Normalize 和 Agent 能力检查。旧 POST `/api/v1/docker/containers` 的同步201保留兼容；前端创建抽屉使用新 Job 入口，不自动降级到旧接口。

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

后端兼容入口、专用队列与结果迁移已实现。新增 fake/Recorder/SQLite 测试覆盖上述边界，并补充同版本但结果不同的负向ACK、真实worker短动作独立执行、running持久化跨排队TTL及日志/SQLite文件秘密哨兵。新旧Job生命周期定向race、controlstore、OpenAPI验证通过；独立只读审查无阻塞发现。该阶段验证不代表生产部署或真实Docker验证。


## 前端 Job 迁移

- 抽屉保留完整参数校验、预览与冻结快照、opening/revision/session 身份防护。有效预览先占本登录会话唯一创建槽；取消预览、修改或卸载只释放尚未派发的确认。确认只派发一次 POST，202 仅表示接纳，不触发 created 或成功提示。
- 提交后可关闭；关闭、切换镜像、组件卸载及路由离开不取消后台任务、不重放请求。迟到回执更新原 session 的 registry，不覆盖新草稿；登出使原 session 失效。关闭与登出清空表单配置。
- registry 只保存 token、generation、runContainer、时间、阶段和安全 Job 证据，不保存名称、镜像、配置或服务端原始文案，不写 local/session storage。镜像创建入口及抽屉提交共享同一个槽。
- DockerView 顶层保护面板在 projects/containers/images 分支及清单 loading/error 之外。核对只读精确 Job 详情与容器清单；清单同名/空白/存在均不作为创建结果。已知完成只有严格 createResult（完整64位小写十六进制 containerId、created:true、与意图一致的 started）有效。
- 有效视图同步、版本化 consumeCompletion 一次，再保存当前视图结果卡并只读刷新。完整 ID 支持换行；说明创建调用确认不保证持续运行。离开视图后的结果留给下一有效视图领取，session abort 独立清空结果卡。
- unknown 只有本轮完整有效任务列表及 inventory 都读取成功且无活动创建证据时，才允许显式风险确认。known failed/interrupted 要精确详情及 inventory 均成功；queued/running/pending/无效结果/读取失败保持保护。弹窗确认前后验证 token/checkVersion，取消恢复焦点。解除只影响本会话，不是取消、补偿、重试或重发；整页刷新、其他标签页与客户端不在范围内。
- 新保护按钮至少44px，沿用字体、颜色、间距与焦点 tokens；窄屏纵排。没有浏览器或真实 Docker 验证，不能把 JSDOM 当作像素、遮挡或设备布局证据。

### 前端验证与后续

参数/路径/设备/端口/卷/命令/网络/GPU/资源预览覆盖保留，并将 POST fixture 改为202 Job。新增保护面板和 DockerView 集成覆盖加载/失败时保护、精确完成领取、跨 scope 迟到结果、session 清理、任务/清单失败不解锁、风险确认版本竞态及取消焦点。定向UI验证及全前端155份测试4466用例通过；Vue typecheck、ESLint、生产构建与540KiB分块预算检查通过（最大分块518KiB，Vite默认500kB提示仍存在）。

E2E 模拟用例已同步适配：`container-create-outcome.spec.ts`、`container-draft-ownership.spec.ts`、`docker-create-input-identity.spec.ts`、`docker.spec.ts` 与共享 `mockApi.ts`；已检查 `resource-races.spec.ts` 无须修改，并保留创建路径优先与旧同步路径拒绝。成功路径必须先断言202不代表成功，再精确 GET Job 核对；未知路径覆盖全局单槽及风险解除；保留参数身份、键盘、secret遮挡及手机几何证据。静态列举不代表浏览器通过；实际执行结果须以相应提交的CI和制品核验为准，未部署生产。
