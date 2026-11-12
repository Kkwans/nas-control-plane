# Docker清单读取隔离：实际CI与部分像素

## 首轮未完成（2026-10-13 21:12 UTC）

`f633432838fea52872f143aace6067c7128372a3`，[CI38467559932](https://github.com/Kkwans/nas-control-plane/actions/runs/38467559932)：1601单元 / 82文件通过，345浏览器通过 / 4失败（5.6分钟）。Go115538154530、Compose115538154115成功，前端115538154334失败。九项新增用例五通过、四失败。

四个失败均为docker-inventory-response.spec.ts的withClockFrames第210行 `clock.runFor: Target page, context or browser has been closed`，由capture第342行等待axe触发：390首次坏清单（调用398）、390缓存错误（431）、390 SSE保留（476）、1440 SSE恢复（495）。日志未提供单次runFor耗时或明确60秒timeout，不能仅凭页面关闭推断原因。官方发布的是UI分片，没有trace/error-context文件可供本轮读取。捕获时钟/axe生命周期待修复，不能放宽断言或把该模块标为通过。

## 已观察的工件

实际查看11幅PNG，其中7个状态有完整状态/axe JSON：1440首次错误、合法空结果、缓存错误、GET恢复、SSE保留，390/1440静默过期。七份完整axe违规为空，准确SHA与fixtureOnly已核对。

另外390首次错误、390缓存错误、390 SSE保留、1440 SSE恢复只保存了PNG，axe失败前尚未写JSON；这些仅作现场图，不做完整视觉验收。390合法空、390 GET恢复、390 SSE恢复未到达，不能声称已查看。

可见错误、缓存/过期提醒及重新加载按钮没有明显裁切；390卡片和1440表格保留上次项目，首次失败显示未知计数而非空清单。SSE坏Docker图同时出现“监控历史写入失败”，准确fixture明确注入MONITORING_WRITE_FAILED并验证两个错误共存，所以该文案不是新发现的误诊。整体等待替代CI与完整捕获。

## 证据来源与边界

两份官方ZIP经过大小/摘要、安全路径、文件类型、展开界限、CRC及跨片唯一性验证，共255条目 / 49,923,573展开字节：
- 分片1工件11850850707：10,495,775字节，SHA256 `94836aa642aa7156a6dfeff31ed4826cb2ac2a7f3ae9c717b5566d9cfbec6cf7`。
- 分片2工件11850895580：6,874,295字节，SHA256 `6c62d4c1a154c5d12bdc7ebd916e94d550743b9795da0d34c3398958e0beac3f`。

全部为只读内存GET/手动EventSource，未访问Docker/NAS或操作容器。静默过期16秒为模拟时钟覆盖，不能替代真实部署的采样与事件流验证。

## 采集时钟生命周期修复附录（2026-10-13 21:17 UTC）

### 已安装依赖的源码证据

本地锁定版本与失败 CI 相同：`@axe-core/playwright@4.10.2`、`playwright-core@1.62.0`。

- `@axe-core/playwright/dist/index.mjs` 的 `analyze()` 使用完整 `runPartialRecursive()` / `finishRun()` 路径；`finishRun()` 在原页面的同一个 context 中创建 `blankPage`，执行 `axeFinishRun` 后在 `finally` 中关闭该页。
- `playwright-core/lib/coreBundle.js` 中 `Clock.runFor()` 先调用 context 的 `addInitScript()` 注册时钟日志，再推进所有 frame 的受控时钟。Chromium 的 `doAddInitScript()` 逐页调用 `page.delegate.addInitScript()`；该注册步骤不会忽略已关闭的目标。后续 frame 求值步骤反而会忽略非 JavaScript 的销毁异常。
- 因此，逐帧推进时钟与 axe 汇总页的正常关闭存在明确竞争窗口，能够解释日志中的精确错误 `clock.runFor: Target page, context or browser has been closed`。本轮没有失败 trace，故该结论是依赖源码支持的原因判断，仍需修复后隔离 Chromium CI 确认，不能声称已复现或排除所有其他故障。

### 修复边界

仅修改 E2E 的 `withClockFrames()` 调度包装，不改产品状态、不替换 axe 完整分析路径、不关闭规则、不改截图或状态断言。

只有同时满足下列条件才等待分析收尾：错误对象的消息精确匹配已观察的 `clock.runFor` 关闭错误；分析期间只新增一个 `about:blank` 页面且它已关闭；原页面未关闭、context 未发出关闭事件且浏览器仍连接。等待最多使用一秒 Node 调度时间，随后必须取得完整分析的成功结果并再次确认原页面/context/浏览器仍存活。分析拒绝、真实页面关闭、其他错误或收尾超时都继续使测试失败。

失败的 tick 不重试，不重复推进已完成的时钟，不恢复真实页面时间。每次采集累计最多仍为 2000 毫秒受控时钟。原有 `clock.runFor(16_000)` 静默过期用例、`performance.now()` 单调新鲜度语义与全量 axe 结果保持不变。未提高 60 秒测试总超时。

### 纯测试与静态检查

新增 `frontend/src/test-support/dockerInventoryCaptureClock.test.ts`，由正常 Vitest 的 `src/**/*.test.ts` 模式发现。测试通过 TypeScript AST 提取真实 E2E 文件中唯一的 `withClockFrames()`，避免复制实现，也不加载 Playwright 测试入口、启动浏览器或监听端口。

21 项纯生命周期回归通过，覆盖完整结果原样返回、辅助页关闭后成功收尾、分析失败、主页面/context/浏览器在等待前与等待中关闭、缺失/多个/非空白/仍存活的辅助页、非 Error 值、错误前缀/后缀不符、一秒收尾上限、2000 毫秒累计预算、无 tick 重放与监听器清理。针对该文件与 E2E 的 ESLint、E2E 单文件 TypeScript 编译、应用 `vue-tsc`、`git diff --check` 通过；Playwright `--list` 仍列出原有九项浏览器用例。独立代码审查与复跑 21 项回归未发现问题。

这些结果验证调度逻辑与静态边界，不是浏览器验收。真实浏览器复跑及完整 PNG / 状态 / axe 工件仍待发布修复后的 CI。

发布前精确快照以 73c08c9 为基线，仅叠加本次三文件：86 文件 / 1654 项单元、全量 lint、类型检查、生产构建及包预算通过。真实 Chromium 修复效果仍待新 CI，未将纯生命周期测试代替浏览器验收。

## 替代 CI 与完整 M29 工件复核（2026-10-13 21:44 UTC）

精确提交 `704b8791e7096312e6616f333603522fcd47a72a` 的 [CI 38470027059](https://github.com/Kkwans/nas-control-plane/actions/runs/38470027059) 已结束：86 文件 / 1654 项单元通过，Chromium 349 通过 / 2 失败；Go 与 Compose 成功，ARM64 发布跳过。Docker 清单的九项浏览器用例全部通过，前轮辅助空白页与采集时钟的关闭竞争在本轮真实 Chromium 中不再出现，M29 的该运行阻塞已关闭。全仓 CI 仍未通过：两项日期帮助用例（390px、100% / 200% 根字号）均在 `resource-alerts.spec.ts:353` 的首次 tooltip 可见断言失败，不能把 M29 通过写成整体通过。

本轮从官方分片下载并直接逐幅查看全部 14 张 M29 PNG：390px / 1440px 各有首次错误、合法空清单、缓存错误、GET 恢复、SSE 保留、SSE 恢复、静默过期七个状态。28 份对应状态 JSON 的 `sourceSha` 均为上述提交，工作树记录为空，全部截图 SHA256 与实际 PNG 字节相符；其中 14 份包含完整 axe 结果，原始 `violations` 均为空。两个 M29 分片分别为 [11851842454](https://github.com/Kkwans/nas-control-plane/actions/runs/38470027059/artifacts/11851842454)（10,255,715 字节）和 [11850604415](https://github.com/Kkwans/nas-control-plane/actions/runs/38470027059/artifacts/11850604415)（6,074,514 字节），每份均低于 32 MiB 下载上限。未复用旧提交的图片冒充本轮证据。

像素观察：首次错误使用未知计数和明确读取失败，合法空清单显示零值；缓存错误及静默过期保留项目和上次更新时间，提醒与重新加载按钮可读；GET 恢复显示新清单并移除旧提醒。SSE 保留图中受限状态与历史写入失败文案可读，恢复图更新 CPU / 时间并恢复正常状态。所查看画面没有明显水平裁切或文字遮挡；390px 总览图片仅覆盖当前视口，不能据此声称已目视检查屏幕下方全部卡片。axe 零违规也不等于覆盖所有无障碍要求。

结论限于该提交、这些 fixture 状态与已保存画面。官方 UI 分片不含失败 trace 或 error-context，未用它们推断日期提示失败的确定原因。本地仅下载、静态读取和查看图片；没有启动本地浏览器、监听服务、PTY、Docker 或 NAS，没有真实 NAS 部署、采样或操作验收。
