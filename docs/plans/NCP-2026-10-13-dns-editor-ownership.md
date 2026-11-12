# DNS 编辑器同页所有权收口

状态：已实现同页所有权隔离，离线验证完成，已通过独立源码复核。范围仅为前端编辑器；不执行真实 DNS 修改、网络设置、探测或 NAS 操作。

## 已确认问题与边界

当前 `InfrastructureView.vue` 的 `ownsPage()` 已隔离卸载和认证更替，但不能区分同一页面的不同编辑意图。

1. **旧预览恢复到新草稿**：`previewDNS()` 捕获草稿 A 发请求；输入框未禁用，用户改为 B，`invalidateDNSPreview()` 只清空值；A 响应仍通过 `ownsPage()`，恢复 A 的 preview。随后确认会发送 A 的 `previewId`，而非 B 对应的预览。
2. **刷新覆盖编辑**：`loadDetails()` 成功后无条件重置 `dnsDraft`、`dnsPreview`、`dnsMessage`。手动刷新在编辑期间返回，或刷新开始后关闭/重新打开编辑器，都会把当前草稿替换为读取值。页面读取序列不能判断草稿归属。
3. **确认重放窗口**：确认请求开始时不消费预览。确认错误时，或者确认成功但后续详情读取失败时，预览仍保留，`finally` 解除 busy 后可再次发送同一个 `previewId`。后端成功时通常已删除该预览，但这是后端拒绝重复请求，不能当作前端允许重发的理由；传输错误也不能证明写入未发生。
4. **回滚句柄依赖辅助读取**：确认收到成功结果后，要等 `loadDetails()` 完成才保存 `changeId`。详情/DNS 辅助读取卡住会令已知可回滚结果迟迟不可见。后续刷新还会清掉结果文案。
5. **动作入口能力缺口**：列表按钮检查 `canPreview/canConfirm/canRollback/!readOnly`，但 `openDNSEditor()` 仅检查页面；`confirmDNS()` 和 `rollbackDNS()` 不复查动作能力或 readOnly。编辑器打开后能力变化不能仅靠外层按钮控制。
6. **回滚失败结果误清句柄**：回滚返回后无条件清空 `dnsChangeId`。当前两个后端正常失败走 HTTP 错误，因此常规失败尚会保留句柄；但 API 类型允许返回带 `errorCode` 的结果，应只在明确成功且匹配目标时清理。

关闭/重开并非目前能在所有写请求中通过普通 UI 重现的独立漏洞：现有 Dialog 在 `dnsPending` 时禁用取消、遮罩、Escape 和关闭按钮。应保留该限制；上面第 2 项的普通读取竞态仍可发生。不要把合成调用测试描述成真实用户已能绕过 Dialog。

补充能力来源：当前详情中的 `dns` 来自 `ProbeDNS`，不授予写权限；独立 `CollectDNSCapability` 仅在可写控制器和当前配置读取成功后开放三项写能力。因此 live capability 失败后回退 embedded details，本版本通常会安全降为只读，并非已确认的“回退意外授权”漏洞。真正缺口是刷新整体失败时仍保留旧 live capability，以及已打开 Dialog 的确认/回滚不复查能力。建议以独立 capability 最近成功结果作为写授权来源，刷新未完成/失败时不沿用旧能力开始新写操作；已发送操作仍单独归属并接收结果。保留 fallback 用于只读展示，不扩展成读取调度重构。

## 最小实施方案

优先局限于 `InfrastructureView.vue` 和对应单元测试，必要时添加一个小型状态辅助模块；不改 Server/Agent/API 契约，不顺便重构整个基础设施页面。

- 编辑器维护递增意图版本，在打开、关闭、草稿修改和显式刷新使预览失效时递增。预览捕获版本、草稿快照和 backend；成功、错误、finally 各自核对归属。草稿即使改回 A，也不能让更早 A 请求重新有效。
- 操作 busy 用独立的请求身份负责释放，不用“当前编辑版本仍相同”作为唯一解锁条件，否则编辑使请求失效后可能永远 busy；旧操作也不能清理新操作的 busy。
- 最小 UX 保留所有 DNS 请求 pending 时禁止关闭；确认/回滚期间禁用输入与相关入口。预览期间可继续允许编辑并丢弃过期结果，也可禁用输入，但仍保留版本检查作为异步安全边界。
- 普通 `loadDetails()` 只更新页面读取快照，不无条件写编辑草稿、预览或结果文案。草稿在明确打开编辑器时初始化；手动刷新保留当前草稿，显式作废已有/在途预览并要求重预览。刷新不能解除正在执行的确认/回滚锁，也不能令其已知结果失主。
- 确认前检查页面、编辑器、当前预览归属和动作能力；同步捕获 `previewId` 并消费预览，然后只发送 `{ previewId, confirmed: true }`。无论成功、失败或读取失败，均不恢复已提交的预览，不自动重试/重新预览/再次确认。结果不明确时明确提示需先核实，不能声称已取消写入。
- 确认结果在后续读取之前落账：明确 applied 且 rollbackAvailable 时保存服务器返回的 changeId 及所属 backend；成功但不可回滚时清旧句柄。失败不能凭空建立新句柄或误清旧句柄。详情刷新失败可单独提示，不得抹去已确认的操作结果。
- 回滚捕获当前 changeId/backend；明确成功（无 errorCode、applied=false、rollbackAvailable=false，且返回 changeId 匹配请求）后，仅清理仍属于该目标的句柄。错误保留句柄；backend 改变时禁止拿旧句柄发送给新后端。关闭再打开可以保留同页已确认的回滚句柄；页面/认证失效仍清空。
- 动作方法与按钮都检查相应能力及 `!readOnly`；backend 变化或能力撤销会作废预览。不能因为关闭编辑器或刷新使令牌过期就假定已发送的确认/回滚被撤销。保持原有 page/session 检查，过期页面不能发布结果、发起复核读取或重放请求。
- 不把详情读取序列直接当作 mutation 所有权：读取可以 supersede，但同页已发送写操作必须保持独立锁与结果归属。这样无需把刷新和写操作绑成同一个全局 latest-wins。

## 后端约束依据

`frontend/src/api/systemInfo.ts` 的确认仅接受 previewId/confirmed，回滚仅接受 changeId。`internal/httpapi/system_handler.go` 将后端错误转换为 HTTP 409，不能靠读取文案推断可安全重试。

`internal/system/dns_static.go` 与 `dns_ugos.go`：预览有 5 分钟寿命；确认核对原始配置、保存备份后写入，成功消费 previewId；回滚核对 changeId 与 afterHash，成功返回同一 changeId、applied=false、rollbackAvailable=false。不能换成当前草稿直接写入，也不能把请求取消解释为后端回滚。

## 验证矩阵（仅离线 mock）

扩展已有 `frontend/src/views/InfrastructureView.test.ts` 的 deferred 模式，保留页面/认证生命周期原用例。先精确检查 API 调用次数和参数，再检查状态：

- A 预览 pending → 改 B → A 成功/失败：B 不变，无旧预览或旧错误；busy 正确释放；A→B→A 同样不能复活旧请求。
- 关闭/重新打开、编辑期间手动刷新及刷新晚到：草稿不被后台读取替换；旧预览/旧提示不侵入新编辑意图。
- 确认双击、响应错误、成功后详情读取错误/延迟：同一 previewId 至多发送一次；成功的回滚句柄及时保存；失败不自动重发。
- 刷新与确认/回滚交错：读请求顺序变化不丢真实结果、不提早释放 mutation busy、不触发第二次写请求。
- 回滚成功、HTTP 错误、结构合法的负结果、返回目标不匹配：仅明确成功清理对应句柄；不能误清其他目标。
- capability 撤销/readOnly/backend 改变：确认和回滚零新增写请求；旧句柄不能跨 backend 使用。
- 各阶段卸载或 session 更替：无迟到状态、复核读取和写重放。

需要浏览器验证时另行安排本地 fixture E2E：所有 `/api/v1/**` 请求 fail-closed 拦截，明确 mock DNS 三个 POST 和自动 Mihomo inspect；没有未匹配 API 直通真实服务。本切片未运行浏览器、监听器或任何真实 DNS/NAS 操作。

## 单独后续事项

`loadDetails()` 的 `Promise.all([systemDetails, DNS capability])` 让辅助 DNS 读取延迟主页面展示，且写操作复核目前也受其影响。可另开“主详情与辅助能力读取解耦”切片；本次只移除操作结果对辅助读取的所有权依赖，不把新请求调度架构一起塞入修复。

## 实施与离线验证记录

- `InfrastructureView.vue` 使用同步编辑版本和不可变请求快照隔离在途预览；独立操作身份负责 busy，保留 Dialog pending 时的关闭限制，并在确认/回滚期间禁用输入。
- `loadDetails()` 不再写入编辑草稿或操作提示；每次读取开始即撤销写能力新鲜度，只有成功的独立 capability 读取才能重新允许写操作。读取失败不能沿用旧 capability；未改动现有 `Promise.all` 调度。
- 确认在发送前消费精确 previewId，失败也不恢复。已知应用结果立即记录 changeId/backend，复核读取在后台进行；详情读取失败单独显示，不抹去已知结果。
- 回滚只允许原 backend；仅无 errorCode、目标/backend 匹配且 applied=false、rollbackAvailable=false 时清理句柄。失败保留已有句柄，无自动写入或重试。
- 保留并调整原有 24 个生命周期/API 信号用例，新增 deferred 同页竞态、单次确认、能力/backend 变化、严格回滚结果与刷新失败用例。目标测试 53/53、全量 63 文件 1088/1088 通过；Vue 类型检查、全量 ESLint 通过。

主任务独立完整63文件1088项单元、全量ESLint、Vue类型检查、生产构建和540KiB预算通过。新增六项隔离浏览器fixture已通过lint、严格TypeScript与发现检查，截图及原始几何在断言前保留；实际浏览器与像素待CI。本地没有运行DNS、网络设置或真实NAS操作。
