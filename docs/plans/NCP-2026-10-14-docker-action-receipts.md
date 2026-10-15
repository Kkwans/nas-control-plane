# Docker 单容器与独立项目生命周期回执

基线 cdcd2ae；限定前端响应边界与反馈，不改 Compose 回执、后端 Completed 语义、公开线协议或引入 Job/轮询。

## 语义与归属

单容器 200 表示命令调用与一次状态读回成功，不保证服务稳定或健康。独立项目 success 表示该项命令及读回未报错；旧 Agent 的 completed 也不能替代逐项目标状态核对。start/restart 期望读回 running，stop 期望 stopped。读回状态不符是有效观测，可能涉及瞬态或并发变更，提示尚未达到目标，不判定命令未执行，不重发写操作。后续清单只读刷新不能证明先前 restart 的执行次数或替代原回执。

浏览器请求来自清单中的规范容器 ID，按请求前固定的 containerId/action 或 projectId/kind/action/完整 containerIds 集合核验。集合允许排序变化，拒绝重复、遗漏、多余、不同目标和子项动作；保留现有可选 errorCode、空名称及开放 state 字符串，拒绝结构错误与 completed=true 含失败项。已知字段重新投影，既不修改输入也不携带附加字段。公开 Docker API 仍可能接受短 ID/名称，不在本片收紧 RPC 身份约束。

## 传输与界面

仅单容器及 standalone 使用专用 transport：同源凭据、精确 200、redirect:error，拒绝跟随跳转回执。派发后网络异常、坏 JSON/结构、未知 HTTP 结果统一显示固定“可能已经执行，先只读核对，勿重复提交”。仅经源码确认的派发前拒绝码/状态组合保留为普通错误，不显示原始响应正文或底层异常。

页面检查实际回执状态；命令被接受但读回不符使用警告。失败、未知和有效读回之后均只读刷新，刷新失败不覆盖原始回执分类，也不产生虚假“已完成”。确认框等待期间防重复派发，冻结项目/目标快照，卸载或会话更换后忽略迟到界面更新。

## 验证

纯 mock 表驱动覆盖结构/归属/矛盾字段、合法瞬态状态、输入不变及响应投影；HTTP 精确状态、跳转、网络/JSON 错误、派发前拒绝以及一次 POST；项目对象/目标集合在等待时变化；Vue 测试覆盖成功、未达目标、未知、只读刷新失败、确认窗口重复点击、卸载/会话变化。仅运行隔离 Node/Vitest、类型检查与构建，不使用监听器、浏览器或 Docker。

## 后端后续建议

另片考虑将 ProjectActionResult.Completed 定义为所有目标都成功且达到本次动作目标，但须先核对协议消费者并同步 OpenAPI。不要简单把状态不符改成 Docker 命令失败；若需独立表达 accepted/observed/reached，应显式扩展协议并处理旧 Agent 兼容性。本片不实现此变更。

## 实现与本地验证（2026-10-14 18:51 UTC）

已实现上述前端切片，包含16项实际组件/隔离请求回归。完整前端3521项测试/121文件通过，lint、Vue类型检查、生产构建与540KiB包预算通过。旧MIME合同测试曾把无body单容器动作的意外415当成已证明未执行；根据HTTP源码该路由不解析JSON，现严格归类未知结果，并保留一次请求、无body、无Content-Type及同源凭据断言。独立项目的合法415拒绝语义不变。

浏览器新增专项与本次远端CI尚待核验；前一数据库TLS修复beb86a9的CI38612535338已通过3411单测、413浏览器，14组实际截图与完整axe零violation但仍有incomplete，不能替代本模块浏览器证据。没有运行真实Docker、Compose或NAS，也没有部署。

## Compose 生命周期观察状态首片（2026-10-15，实施前边界）

先独立修正 Agent 内 `parseComposePS` 与生命周期观察判定，随后另片收敛前端回执归属与未知结果提示。当前零值记录、缺失 State 和未知状态的 `Running=false` 可被当作停止，不能据此宣告完成。

- 保留现有数组及 NDJSON；State 必须显式为非空字符串，可选 Running 出现时必须为 bool。明确 running/false 或 stopped、exited、created、dead/true 的矛盾整体拒绝；不以 Running 补造 State。拒绝 null、坏字段类型、ID 与显示名均为空的记录、重复非空 ID，以及已知语义字段的重复键或大小写别名；允许普通未知 CLI 字段。Service 回退 Name（去单个前导 `/`），同服务名多副本保留，不新增 ID 长度约束。
- start/restart 只由全部明确 running 确认；stop 只由全部 exited、created、dead 或既有兼容 stopped 确认。这仅表示观察到非运行，不代表健康停止。paused、restarting、removing、未来未知或缺失状态不能确认任何动作，汇总不伪装为 stopped；空集合仍不能确认完成。Manager 的具名 fake/provider 继续兼容缺省 Running，但必须以 State 判定。
- 不改命令、查询次数、公开字段、RPC/HTTP 合同、重试或部署。命令完成与一次状态观察不证明服务健康、稳定、配置服务完整性或重启次数；VERIFY_FAILED 可能发生于命令之后。前端后续另片去除旧重试建议、绑定冻结 project/action 并拒绝重定向重发。
- 先以纯 parser 与 fake LifecycleRunner 在原生产代码复现失败，再修复和运行定向 race、编译/vet；不运行 Docker、helper、监听器、浏览器或 NAS。独立审查与实际验证结果接续记录，不把本段计划当成已通过。

官方依据：[Compose ps 文档](https://docs.docker.com/reference/cli/docker/compose/ps/) 列出 NDJSON 与 State；[v2.39.4 ps 实现](https://raw.githubusercontent.com/docker/compose/v2.39.4/pkg/compose/ps.go) 逐容器投影 Service/State；[v2.39.4 formatter](https://raw.githubusercontent.com/docker/compose/v2.39.4/cmd/formatter/container.go) 直接输出 State，没有 Running 布尔字段。数组为本仓库已支持格式，未以版本猜测替代兼容测试。

本地验证接续：新增纯 parser 与 fake LifecycleRunner 回归在原生产文件 overlay 上实际失败（包括 null、缺失/矛盾 State、别名重复及未知状态误报）；修复后完整 `internal/compose` 测试与 race ×20、该包 vet、全部 cmd 编译通过，diff 检查通过。已检查包内测试仅使用 fake 与临时文件，不调用 OSRunner、Docker、helper 或监听器。独立审查无阻塞，并独立复跑生命周期/parser 聚焦测试通过；远端 CI 尚待后续提交核验，未作 NAS 或真实 Docker 验收声明。

## Compose 前端回执与未知结果切片（2026-10-15）

在后端观察状态修正 a9691719 之上，将 Compose 生命周期请求纳入已有专用传输：精确 200、禁止自动跳转、固定派发前 projectId/action/workingDirectory/configFiles，按本次项目与动作验证回执。配置决定真实 Compose 目标，因此不绑定旧清单 containerIds，也不以服务名称把回执副本重新绑定到旧容器。

已知字段按自有属性重新投影，保留公开 output 字符串合同，但不展示或附带到错误。服务至少包含非空名称或 ID；允许缺省/空 ID、同名副本，拒绝重复非空 ID、空状态及 running/state 矛盾。completed=true 必须非空且全部服务达到动作的明确状态，汇总必须对应 running/stopped；completed=false 的明确汇总也须与服务观察一致，未来非空汇总/服务状态保持开放兼容并仅作未完成警告，不能成为成功证据。

仅保留已核验的中间件拒绝、CONTROL_INPUT_INVALID/400、COMPOSE_LIFECYCLE_INVALID/400 与请求体 413/415 的固定文案。COMPOSE_LIFECYCLE_UNAVAILABLE 在 RPC service 中可由执行后的任意 provider 错误映射产生，不能当作派发前拒绝；VERIFY_FAILED、FAILED、超时和通道失败均为未知结果，禁止使用响应正文中的重试建议。Compose 路由没有明确暴露 protocol-mismatch/409，未把该组合加入其拒绝表。

界面只报告命令受理与一次读回观察，不声称健康、稳定、配置完整性或重启次数；未完成/未知显示只读核对警告，刷新失败不改写原分类。保留确认前项目配置快照、会话/卸载保护及防重复派发，不新增未知锁、轮询、Job 或任何新写操作。

本片本地验证：完整前端 3931 项测试/140 文件通过；Vue 类型检查、修改文件及完整 ESLint、生产构建及 540KiB 包预算通过，diff 检查通过。独立审查复跑类型检查与 API/组件相关 225 项测试通过，无剩余阻塞。全局 lint 首次运行遇到并行编写中的浏览器专项文件未使用符号，其作者完成后完整复跑通过；未运行浏览器、监听器、真实 Docker/Compose 或 NAS，本地结果不替代远端 CI/浏览器证据。

收尾补充：项目操作 finally 仅释放自己持有的 pending 对象（保存 Vue ref 读取后的对象身份）。当前组件没有会话切换清空 busy 的路径，因此旧请求未结束时新会话操作仍被阻止，未声称存在可达的旧请求覆盖新 pending 竞态，也未新增会话重置机制。新增真实流程回归：切换会话后旧请求不重发、迟到结果不通知/刷新，原请求结束释放自身占用，随后新会话操作独立完成。补充后的组件 27 项测试、类型检查及修改文件 lint 通过；此前完整 3931 项结果对应补充前版本。

最终所有权保护落地后复跑：完整前端 3932 项测试/140 文件、Vue 类型检查、生产构建与 540KiB 预算通过。新增 Compose 浏览器专项共 4 例，静态 ESLint、严格 TypeScript、Playwright 用例发现通过；覆盖 390/1440 宽度的真实确认框、完成观察与错配/409 未知结果、冻结配置及只读刷新不重放。截图、完整 axe、请求账本和源码哈希的实际浏览器证据等待提交后的 CI，未提前声明通过。确认框按钮保留实际尺寸记录与命中检查，44px 断言仅覆盖既有警告关闭按钮，未将默认确认框尺寸宣称为 44px 验收通过。
