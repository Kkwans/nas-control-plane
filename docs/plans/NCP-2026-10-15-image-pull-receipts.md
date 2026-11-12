# 镜像拉取完成回执归属

基线 2728e68。纯 fake Agent 在原生产代码复现：请求 alpine:latest，返回 nginx:latest/completed=true 后，Job 被错误标记 completed、artifact present。正常 ImageManager 会返回 normalizeImageReference 处理后的请求原值；本问题是消费边界缺少验证，不代表正常 Docker 必然返回错目标。

## 范围与合同

- 共享纯验证器复用现有 normalizeImageReference：请求首尾 TrimSpace，保留已有长度、内部空白、tag/digest 检查，不引入完整镜像语法、默认 tag、registry 别名或大小写规范化。
- 仅当 completed=true 且回执 Reference 精确等于规范化后的冻结请求才信任完成。回执空白、未完成或不同引用均不确认结果；回执首尾空白也拒绝，因为真实 Manager 必返回已规范化的引用。
- 普通 RPC 解码与流式 completed 帧共用验证器，失败返回零结果和 AGENT_RPC_RESPONSE_INVALID。流式进度及原有 EOF/取消/超时传输映射保留。
- HTTP Job 对包括替代 Agent 在内的结果再验证，失败使用 JOB_EXECUTION_INTERRUPTED、interrupted 与 artifact unknown，保留已下载进度。不把未知结果说成未执行或自动重拉。
- 不改公开字段、接口、Job 引擎或持久化格式。终态保存失败仍只重存待保存结果，不重放拉取。artifactState 仍是当前视图状态，不新建持久化字段。

## 验证边界

只使用纯 structpb、fake stream、fake Agent 与 snapshotJobStore。包中存在真实监听器测试，因此仅运行已检查的命名测试集合；不运行全 HTTP/RPC 包测试。覆盖精确 tag/digest、请求首尾空白、别名/大小写/错目标拒绝、缺失/错误类型/空白/未完成回执、进度后完成与传输结束、Job 持久化重存和取消/超时。不得启动本地浏览器、监听器、Docker、用户电脑或 NAS；不部署。

本地已通过三包 TestImagePull 命名集合 race ×20、go build ./...、三包 go vet、api/openapi 全部文档合同测试和 git diff --check；新增 nil 流消息与终态不可覆盖回归后已完整复跑。未运行包含监听器的全 HTTP/RPC 包测试，未进行真实 Docker/NAS 验证或部署；远端 CI 等待独立审查与提交后运行。
