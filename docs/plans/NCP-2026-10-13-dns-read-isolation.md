# 系统详情与辅助 DNS 读取隔离

日期：2026-10-13。状态：前端隔离已实现，纯 mock 验证已执行；未操作真实 NAS/DNS。承接基础设施生命周期和 DNS 编辑器所有权切片；只调整前端读取调度、状态与反馈，不改 API、Agent、DNS 写协议或 NAS 配置。

## 已确认问题

`InfrastructureView.vue::loadDetails` 在 `Promise.all([requestSystemDetails(), requestDNSCapability().catch(() => null)])` 后才发布详情、解除主加载状态并启动 Mihomo 检查。辅助 DNS 一直等待时，已返回的系统详情仍被遮蔽；详情失败时，独立成功的 DNS 结果也不会发布。DNS 异常被吞成 null，页面没有明确的能力读取错误，手动刷新注册入口仅检查详情错误，会误报本次 DNS 失败为成功。

现有 24 项生命周期/API 信号用例与扩展后的 53 项测试必须继续保护页面/认证代次、草稿、预览、已知写结果。不要将测试目前依赖的 Promise.all 等待顺序固化为产品要求。

## 最小调度与状态

- 保留详情的 `details/loading/errorMessage/detailsRequestGate/detailsController`；新增 DNS 专属 `dnsCapabilityLoading/dnsCapabilityError/dnsRequestGate/dnsController`。DNS 快照和 `dnsCapabilityFresh` 继续分离，失败保留快照仅作过期展示。
- `loadDetails` 可保留为并发协调入口，内部调用两个独立 loader；新增 `loadSystemDetails` 和 `loadDNSCapability`，各自开始、发布、失败、finally 只操作自己的读取状态，并返回本次的成功、失败或失效结果。页面首次加载和完整刷新启动两项，发布不能等待聚合完成。
- 详情成功即解除主 skeleton、发布页面并启动既有 `checkMihomo(false)`；详情刷新开始负责取消/清理旧 inspection。DNS-only 重试不重读详情、不取消/启动 inspection。系统详情专属重试只请求详情；保留原有详情成功后的 inspection 行为。
- 每个 loader 独立合并 page session 和自身 AbortSignal，使用 `ownsPage + 本资源最新序号 + !signal.aborted` 判断发布、错误与 finally。新 DNS 读取只取消旧 DNS，新详情读取只取消旧详情。完整刷新和写后复核同时 supersede 两项。
- dispose 同步失效并取消三类读取、清空新状态，保留已有敏感编辑清理与注销。忽略 abort 的旧响应也不能落地或解除新 busy。

## 权限、草稿与已知写结果

- 写权限唯一来源仍为最近成功的独立 DNS capability；`details.dns` 和旧 live snapshot 只能用于展示，不能授予 preview/confirm/rollback。建议计算权限时直接读取 `dnsCapability` 并要求 fresh，避免未来改动意外把 fallback 变成授权。
- 开始 DNS 读取立即 fresh=false 并作废旧/在途预览；失败维持 false；本资源最新成功才 fresh=true。完整刷新显式作废预览；系统详情-only 重试不把已确认的独立 DNS 能力撤销，也不改编辑草稿。
- 详情成功 + DNS 等待/失败：正常显示设备、网络、存储与服务；DNS 标注读取中/失败，写入口禁用。没有独立成功能力时，即便 embedded DNS 带可写字段也不能写。
- 详情失败 + DNS 成功：两个读取状态各自保留。无详情时显示详情专属错误和重试，不捏造详情面板；已有详情则保留旧详情及刷新警告。新独立 DNS capability 是权威，可恢复原有编辑器的写能力，不把详情失败当作 DNS 失败，也不从 embedded DNS 恢复权限。
- 所有读取均不得覆盖 `dnsDraft/dnsMessage/dnsChangeId/dnsChangeBackend` 或恢复已消费预览。原来的 revision、previewOwner、独立 mutation id/busy、backend 绑定和一次性消费不变。
- 确认/回滚明确成功先保存已知结果，再后台启动两项复核。复核立刻撤销 DNS 新鲜度；仅新 DNS 成功即可恢复允许动作，不能再等待详情。旧刷新即使在写后返回也不得覆盖任一快照；写操作不重放。模糊写结果仍保留原有“先核实”提示，不新增自动补偿或写重试。

## 最小 UX 与刷新聚合

- 主 skeleton/初始错误仅取决于详情；初始错误按钮和旧详情刷新错误按钮均为“重试系统详情”。
- 页面级可见 DNS 状态条（放在详情条件块之外，确保初始详情失败也可见）显示“DNS 能力读取中”或“DNS 能力读取失败，修改暂不可用”，错误时提供“重试 DNS 能力”。保留快照时明确显示上次读取信息，不能显示为当前已核实配置。
- 已打开 Dialog 同样显示 DNS 正在复核/读取失败及目标读取重试，避免遮罩挡住唯一恢复入口；能力恢复不自动预览或确认。不要复用 `dnsMessage` 承载读取异常，以免抹掉已知写结果。
- `InfrastructureDnsPanel` 的“支持安全修改”和说明、概览 DNS 信号也必须感知 capability 新鲜度；只隐藏编辑按钮但继续宣称支持修改会产生矛盾。优先添加简单的加载/错误/新鲜度呈现，不把暂时未知伪装成后端本身只读。
- 手动刷新继续注册一个 handler。聚合等待本次两项读取都结束：任一当前有效失败则 reject，包含资源名；两项成功才完成成功反馈。可以等待聚合，但各资源 UI 必须早已独立发布。仍在等待的资源不能被宣称成功，不在本片引入任意超时。
- 聚合使用各请求捕获的结果/序号，结束时忽略被新读取或失效页面取代的结果，不读可变全局 error 字段来归罪另一代请求。保留当前 registry 对失效页面的静默完成语义；不扩展共享刷新接口，也不把已取消旧刷新宣称成最新两项已经完成。

## 离线验证矩阵与边界

1. 详情成功 DNS pending：主 loading=false、详情可见、inspection 已发起；DNS fresh=false，所有 DNS 写入口零调用。DNS 失败不清详情，显示专属警告。
2. 详情失败 DNS 成功：DNS 数据保留，详情有专属错误；已有快照与首次加载两种情况；embedded DNS 的伪可写字段从不成为授权。
3. 两种目标重试只增加对应 GET；DNS-only 不触发 inspection、不覆盖草稿/已知写提示；详情-only 不消耗独立能力。
4. 各资源 A慢B快、旧成功/旧失败/旧finally，交叉两资源完成顺序；卸载与认证更替取消全部信号，迟到结果无状态和无二次检查。
5. 手动刷新两成功、各自失败、双失败、其中一项等待、刷新被新重试/写后复核取代；注册统计为一个 handler，失败不误报成功或从新错误中拼旧结果。
6. 已知确认/回滚结果立即保存；复核 DNS pending 阻止新写，DNS 先成功而详情仍 pending/失败可恢复合法动作；旧读不覆盖新数据、预览不复活、写次数不增加。
7. 现有“确认结果先于复核读取”用例应改为让 DNS deferred，不能仅挂起详情后仍断言回滚必被阻止。现有 DNS 失败手动刷新用例应改为预期 reject。

实施只需视图、对应单元测试与必要的 DNS panel 展示参数；无需新增通用状态机、超时策略或后端改动。完成后跑纯 mock 测试、lint、类型检查与构建；本次实施未运行任何真实 NAS/DNS 操作、浏览器或监听服务；纯 mock 单元验证与静态构建结果见下。


## 实施记录（2026-10-13）

- `InfrastructureView.vue` 使用独立详情/DNS loader、sequence gate、controller 与错误/busy 状态。详情先发布并启动既有 Mihomo 检查；DNS-only 重试不影响详情和检查，详情-only 重试不撤销独立 DNS 能力。
- 一个手动刷新 handler 并行启动两项，并等待本次捕获的结果；在聚合结束再次检查各自序号和 page/session 所有权。忽略已失效结果，不读取全局错误来决定旧刷新失败；空错误消息也有稳定的资源级回退文案。
- DNS 写权限直接读取 fresh live capability。读失败保留过期快照，页面与 Dialog 显示目标重试；DNS panel 和概览信号不将过期信息宣称为可安全修改。既有编辑意图、mutation busy、一次性预览消费、backend/changeId 绑定与已知写结果均保留。
- 原确认复核测试改为延迟 DNS，而非错误依赖详情阻塞；覆盖两资源独立完成、初始/已有详情失败、目标重试、双向取消隔离、旧响应/错误/finally、聚合失败/等待/后续取代、会话清理、已知写后的 DNS 先恢复与旧 DNS 不覆盖新快照。
- 未改 API、Agent、DNS 写协议或任何安全设置；未执行浏览器、监听服务或真实 NAS/DNS 操作。浏览器 fixture 的独立离线修改与后续执行由主任务单列，不属于本记录的已执行验证。
- 最终离线验证：`pnpm test` 63 个测试文件 / 1110 项通过，其中 InfrastructureView 75 项由独立只读审查复跑通过；`pnpm lint`、`pnpm build`（含 Vue 类型检查、生产构建和 bundle budget）通过，`git diff --check` 通过。仅保留 Vite 既有 >500 kB 提示，最大 chunk 518.0 KiB，低于强制 540 KiB 预算。

主任务对本模块精确隔离副本独立运行63文件1110项单元、Vue类型检查、生产构建与540KiB预算通过。新增四项浏览器fixture及既有两处契约适配通过lint、严格TypeScript和14项发现检查，实际浏览器仍待CI；已知DNS Dialog重复banner的axe失败属于另一个正在修复的切片，未删除或降级其断言。
