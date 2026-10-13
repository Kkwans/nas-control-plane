# NCP SQL 实际截图复核（只读）

## 最新结论（c4f4473，2026-10-13 11:28 UTC）

已完成新产物的 29 张 SQL PNG 原始像素复核与 9 份 capture-state JSON 校验。先前的移动工具栏 sticky-header 遮挡**验收证据缺口已关闭**：规范全页图与主动滚动视口图均完整显示模式标签、选择框和执行按钮；所有 9 种捕获状态的 135 个控制器命中采样均通过。此结论是截图与测试证据闭环，不表示修复了已证实的产品 CSS 缺陷。完整新证据及剩余边界见本文末尾；下方保留原 f9c3fc3 发现与中间审查，供追溯。

复核时间：2026-10-13 UTC。范围：M14 B1 SQL 结果模式与执行反馈；实际打开并查看 11 张 CI PNG 原始像素，并对照该提交的 E2E、组件及样式源码。

## 结论

结果语义、未知影响行数、RETURNING 后刷新失败警告以及两个 SQL 确认框的截图内容符合 fixture 预期；未见这些文字横向溢出、按钮文字不可读，或 SQL 预览内容与 fixture 不符。

但本批截图不能获得无保留的「移动工具栏无遮挡」视觉通过结论：4 张 18px 字号的移动结果截图顶部被 sticky 面包屑覆盖，结果模式标签/选择器的上缘部分被遮挡；2 张 320px/15px 截图的工具栏标题也未完整显示。这是确定的截图证据缺口，目前不是已复现的产品控制不可达缺陷。

## 来源与 CI 边界

- 产品提交：`76a461c376c702e7b30d5dd18627ccf943a93235`。
- 截图/CI 提交：`f9c3fc34dba7a09520098380d264643832b07f40`，是上述产品提交的直接后继；两者差异只在 `frontend/e2e/monitoring-history.spec.ts`，无 SQL 产品代码变化。
- [NCP CI run 38387732185](https://github.com/Kkwans/nas-control-plane/actions/runs/38387732185)，attempt 1，2026-10-13T11:08:10Z 完成，GitHub 返回 `completed / success`。
- 本次复核独立读取该 run 和 jobs：Frontend checks、Go checks、Compose config 成功；Publish ARM64 release 跳过。并读取 Frontend job 115263558696 日志：355 项单元测试、148 项 E2E 通过；`ConfirmDangerDialog.test.ts` 5 项通过。这里是 CI 结果复核，未在本地重新运行浏览器或测试。
- [截图 artifact 11806559065](https://github.com/Kkwans/nas-control-plane/actions/runs/38387732185/artifacts/11806559065)，名称 `ncp-fixture-ui-f9c3fc34dba7a09520098380d264643832b07f40-1`，11,446,156 bytes；GitHub SHA-256：`4801d55cbe5b54b88280dce9bda56394a01132cf1f1762371ca203291d08a5c5`。
- 使用已校验的共享解包目录，未重复下载。下载复核方确认 ZIP SHA-256 匹配，CRC、路径穿越、符号链接、文件类型与大小检查后解包；139 文件（135 PNG、4 JSON），展开 11,837,400 bytes。本复核另行读取 GitHub artifact 元数据确认 ID、run、SHA、大小及 digest 对应。
- 共享解包根目录：`/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/`。

## 逐类实际观察

### 返回行 + rowsAffectedKnown=false（4 张）

视口 320/390 × 900，基准字体 15/18px。截图均显示「返回 1 行」、`id=1`、`value=fixture-updated` 和「执行完成；数据库未提供可用的影响行数。」没有把 fixture 的 `rowsAffected: 0` 描述为可信的零影响行数。说明明确包含 RETURNING 应选返回行、返回行模式也会写入、500 行仅是展示上限等信息，均可完整换行阅读。执行按钮可读；模式选择器的顶部遮挡限制见下节。

### RETURNING 已执行 + 表数据刷新失败（4 张）

上述四种组合均保留返回行内容，并以独立橙色警告显示「SQL 已执行，表数据刷新失败。」、重新读取而非重跑 SQL 的提示和 `database_not_found`。未把刷新失败画成 SQL 执行失败。警告、未知行数说明及返回数据未见截字或互相遮挡。截图无法单独证明请求次数；E2E 源码与成功 CI 共同提供请求计数证据：第一次 query 1 次、rows 2 次；用户第二次显式执行后 query 总计 2 次、rows 总计 3 次，并断言 query error 不出现、执行按钮重新启用。

### 旧响应缺少计数标志（1 张）

1280 × 900 视口、18px 字号下，自动模式、SQL、执行成功与未知影响行数提示清晰可见。没有显示 fixture 中的「影响 7 行」。E2E 另断言自动模式不发送 `resultMode`。本批没有此状态的 320/390 截图，不延伸声称其移动端已单独视觉验收。

### SQL 确认预览（2 张）

320/390 × 900、18px 字号下，风险说明、执行模式、原始 SQL 预览、取消/确认执行按钮均在截图内可读。`DELETE FROM settings; -- comment` 后面的实际换行得到保留，下一条 `DELETE FROM other_table;` 没有并入前一条行注释；80 个连续 x 在两种宽度均换行，未冲出预览框。320px 的 `-- comment` 自然软换行为两行，但第二条 DELETE 仍从后续新行起始。没有内容快照不匹配的可见迹象。

对应 E2E 还校验 `textContent === 原 SQL`、`pre-wrap`、`overflow-wrap:anywhere`、`min-width:0`、长 token 至少两行、注释/后续语句行框、确认前零 query 请求、取消后仍零 query 请求。截图只证明当次展开状态；取消行为来自 CI 断言。

## 需要补全的截图证据

1. `fixture-sql-rows-unknown-320-font-18.png`、`fixture-sql-refresh-failed-320-font-18.png`、`fixture-sql-rows-unknown-390-font-18.png`、`fixture-sql-refresh-failed-390-font-18.png`：面包屑区域出现在图片自身 y≈0–58，遮住工具栏标题及模式标签/选择器上缘。图片自身坐标不是浏览器文档或视口坐标。
2. `fixture-sql-rows-unknown-320-font-15.png`、`fixture-sql-refresh-failed-320-font-15.png`：图片 y≈0–32 为面包屑残片，工具栏标题不完整；模式选择器本身可读。
3. 390px/15px 两张和桌面 legacy 一张未见同类标题覆盖。
4. 8 张移动结果图均残留 CodeMirror `value / values` 自动完成列表；它没有挡住结果，320px 图中列表到达裁图右边。无法仅凭列表推断捕获时焦点，不把它当作已证实焦点 bug。

源码依据（均指被复核的 f9c3fc3 提交）：

- `frontend/e2e/database-query-results.spec.ts:62–65` 直接调用 `.sql-workbench.screenshot()`，不显式规范 scrollY，也不记录截前/截后的几何和焦点。
- 同文件 `expectWorkbenchFits:47–60` 检查边界与宽度，以及说明文字行框；没有 sticky-header 遮挡/命中测试。
- `frontend/src/layout/AppShell.vue:351,391`：topbar 为 `position:sticky; top:0; z-index:30`，移动端最小高度 58px。
- 4 张大字号 workbench 裁图高 1024/1047px，超过 900px 测试视口；元素裁图不能被直接当作正常完整视口证据。
- artifact 的 4 个 capture-state JSON 全部属于日志测试，没有 SQL 的 scrollY、header/toolbar 边界、activeElement、截前/截后坐标。故无法定论遮挡属于截图自动滚动还是正常交互后的永久不可访问状态。

建议以测试证据修复为起点，不先修改产品 CSS：保留真实选择/执行/取消和请求计数断言；另捕获 scrollY=0 的规范全页图，以及把工具栏放到真实 sticky-header 底部以下的显式滚动视口图；记录两种状态的坐标与焦点、控制器命中结果，再判断是否有产品问题。可沿用 `frontend/e2e/resource-readability.spec.ts` 的日志全页/滚动视口/JSON 模式。不要通过隐藏 sticky header、强制 blur/focus 或去除菜单来掩盖原状态。

此结论及确切路径已提供给负责 SQL 截图证据的实现方；本报告不宣称尚未完成的后续修复或新 CI 已通过。

## 共享 ConfirmDangerDialog 边界

- 本次 SQL 变更在共享组件只增加可选 `details` 插槽。现有使用方还包括 `UsersView.vue`，数据库的其他危险操作也复用它。
- 已阅读新增 5 项兼容单测，并由同 SHA 的 Frontend 日志确认通过：不传插槽、传插槽、busy 禁用、名称确认与关闭清空、空说明/空列表。
- 这些单测 mock Element Plus，因此不能等价替代真实弹窗的外观/焦点测试。
- 本 artifact 未提供用户删除、数据库行删除等旧用法的打开状态截图；数据库旧 DELETE 的取消 E2E 存在但不产图。没有据此声称所有旧弹窗已获得像素级回归验收。
- SQL 新测试使用捕获附件与布局/内容断言，而非 `toHaveScreenshot` 基线比对；不存在本批 SQL 图与历史基线逐像素相等的证明。CI 中其他已有 screenshot regression 成功，也不自动补足此项。

## 操作与验收限制

本工作仅从现有文件读取源码/PNG、读取 GitHub CI 元数据与日志，并写入本仓库外报告。没有产品修改、提交、部署、NAS/生产访问、数据库操作、本地浏览器或监听端口；没有把 mock fixture 结果当成真实 NAS/数据库验收。

## 已查看图片清单

以下均实际用图像工具打开；完整路径、像素尺寸和本地 PNG SHA-256 随后列出。

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-SQL-确认预览在-320px-大字号下保留注释后的换行-chromium/ui-captures/fixture-sql-confirmation-newline-320-font-18.png`
  - 320 × 900px；SHA-256 `7ef8b4bbbcd1176284bb92e494040147507118541adad5446d5e3f1b473c63c3`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-SQL-确认预览在-390px-大字号下保留注释后的换行-chromium/ui-captures/fixture-sql-confirmation-newline-390-font-18.png`
  - 390 × 900px；SHA-256 `d26dd22717ca6c0180f6c3b8fe069006e1dbb8ddd93c80f71b93439b0f31db35`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-旧响应缺少计数标志时显示未知，自动模式不发送-resultMode-chromium/ui-captures/fixture-sql-legacy-affected-unknown.png`
  - 992 × 844px；SHA-256 `edb57002b5166ebb5d2783ecf0f5d15ee005e0e85ef6a51f293033a999c79a90`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-320px-15px-字号下保持可读-chromium/ui-captures/fixture-sql-refresh-failed-320-font-15.png`
  - 296 × 877px；SHA-256 `4ec429645298deed3f1d509665871aacd20729c84af76a0a5108bc676f75b113`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-320px-15px-字号下保持可读-chromium/ui-captures/fixture-sql-rows-unknown-320-font-15.png`
  - 296 × 877px；SHA-256 `5f236dcd0fde85db88ff4720eae0875ad0828587aa4b9fac2e919d5ec087bb0b`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-320px-18px-字号下保持可读-chromium/ui-captures/fixture-sql-refresh-failed-320-font-18.png`
  - 296 × 1047px；SHA-256 `bf75eaa0d6881aeefaecfa85bd2eb04b2fe8503365217725d7229f2216b76f7a`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-320px-18px-字号下保持可读-chromium/ui-captures/fixture-sql-rows-unknown-320-font-18.png`
  - 296 × 1047px；SHA-256 `f010000589d3815e18a0a4a7f1c89ff2dccabfda0ea29b35b2e4e6b7d161c2fd`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-390px-15px-字号下保持可读-chromium/ui-captures/fixture-sql-refresh-failed-390-font-15.png`
  - 366 × 857px；SHA-256 `18173b9e13daacf964a0cbc9a9b50d47785c82ff5f3512c90cb1e8de3a5dfbab`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-390px-15px-字号下保持可读-chromium/ui-captures/fixture-sql-rows-unknown-390-font-15.png`
  - 366 × 857px；SHA-256 `0644c0f0b4911fb3023666ede5ba80a00e86cfe4ebad7a7699293fbd5de7127b`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-390px-18px-字号下保持可读-chromium/ui-captures/fixture-sql-refresh-failed-390-font-18.png`
  - 366 × 1024px；SHA-256 `d354b6da5719171b06bb7db5f67c22dcd0df4d24d745d636ce5ec76eaa3514d3`

- `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-f9c3fc3/database-query-results-返回行、未知影响及刷新失败在-390px-18px-字号下保持可读-chromium/ui-captures/fixture-sql-rows-unknown-390-font-18.png`
  - 366 × 1024px；SHA-256 `0f8b32c75d1b298043d21e52a4b7324b8639aea2a48bc0e7422cb5d1d3c73cd3`

## 后续捕获 helper 独立源码复核（2026-10-13 11:18 UTC）

已审阅冻结后的 `frontend/e2e/database-query-results.spec.ts` 工作树版本，文件 SHA-256 `572437843278ae01f6f518926f15a971b4d52b9ee1113fd35e815af67c50d192`。结论：源码方案通过，无阻塞发现；尚不代表新截图通过。

- 捕获真实执行后的 viewport，再将工具栏显式放在实测 sticky-header 底部下方 12px 目标位置，最后从 scrollY=0 捕获规范全页图。
- 请求滚动范围做 0/max 钳制并取整，轮询到达目标，避免分数像素差异导致无关失败。
- label/select/execute 各做中心及四个内侧命中采样，同时要求视口边界和 header 下方位置。
- 保存 initial/scrolled/canonical/afterCapture 的滚动坐标、几何、焦点、编辑器 ARIA、自动完成菜单状态；文件名 `*-capture-state.json` 与现有 CI artifact 上传规则一致。
- 无 blur/focus/Escape、遮盖或隐藏 topbar、删除自动完成菜单、产品 CSS 修改。真实 query 次数、返回内容、模式、取消测试断言所在的全部 test body 与原 HEAD 逐字节一致。
- 本复核独立完成：严格 TypeScript 编译、单文件 ESLint、7 项测试发现、diff 空白检查均通过。未启动浏览器/服务器。
- 后续需新提交 CI 产物验证真实 post-execution 状态、规范全页图与有意滚动态控件命中，旧 artifact 的上述结论保持不变。

## c4f4473 实际 CI 产物复验与闭环

### 完整来源链

- `76a461c376c702e7b30d5dd18627ccf943a93235`：M14 B1 SQL 产品实现。
- `f9c3fc34dba7a09520098380d264643832b07f40`：[原 CI 38387732185](https://github.com/Kkwans/nas-control-plane/actions/runs/38387732185) 与 artifact 11806559065，产生上述初次证据缺口。
- `d982b50`：中间文档提交。
- `c4f447300c083acc56f177c4b067dad609c052d4`：只修改 SQL E2E 捕获 helper 与复核文档，没有 SQL 产品/样式变化。helper 文件 SHA-256 与独立审查冻结版完全一致：`572437843278ae01f6f518926f15a971b4d52b9ee1113fd35e815af67c50d192`。
- [新 CI 38389527278](https://github.com/Kkwans/nas-control-plane/actions/runs/38389527278)，attempt 1，2026-10-13T11:20:22Z 开始，11:23:17Z 完成，`completed / success`。独立读取 run、jobs、Frontend 115269572829 日志：Frontend、Go、Compose 均成功；355 单元测试、148 E2E 通过；共享 ConfirmDangerDialog 5 项单测通过；发布任务跳过。
- [新 artifact 11807841631](https://github.com/Kkwans/nas-control-plane/actions/runs/38389527278/artifacts/11807841631)：`ncp-fixture-ui-c4f447300c083acc56f177c4b067dad609c052d4-1`，13,260,416 bytes，SHA-256 `8f0b06028cbacbee5816bc116b7cb1189081683688f57e05d6805ff4f73e9234`。下载后独立比对大小与 digest，并检查 CRC、路径穿越、重复文件名、符号链接、PNG/JSON 类型与展开大小限制后，解包到新目录。
- 解包目录 `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-c4f4473/`：166 文件（153 PNG、13 JSON），展开 14,245,515 bytes。其中 SQL 集合为 29 PNG、9 JSON。
- 所有 38 个 SQL 文件的确切路径、字节数、SHA-256，以及 PNG 尺寸，记录在仓库外 `/workspace/scratch/a2c31a226a28/ncp-artifacts/sql-review-c4f4473-manifest.json`。

### 实际打开与验证范围

实际打开全部 29 PNG：8 个移动结果状态 × 3 种捕获（执行后视口、主动滚动视口、回顶规范全页）=24 张；desktop legacy 对应 3 张；320/390px 确认框 2 张。9 份 JSON 均独立读取校验。

- 移动端：320/390px，15/18px 字号，`rows-unknown` 与 `refresh-failed` 两种状态。控件文字完整、原 SQL、模式说明、未知影响行数、返回 `fixture-updated` 及橙色刷新失败警告仍符合 fixture。
- 18px 字号结果表/警告在部分 900px 视口图底部自然延续到视口外；规范全页图确认完整内容。此处是正常垂直页面长度，不把单个视口外内容误称为裁切缺陷。
- legacy：1280px/18px，自动模式、执行成功及未知影响行数可读，未出现「影响 7 行」。
- 两个确认框继续保持注释后的实际换行和 80 字符长 token 的换行，取消与确认执行按钮完整。未见 SQL 快照内容错误。

### 几何与控制器命中证据

9 份 JSON 的 initial scrollY 均为 0；canonicalState 与 afterCaptureState 的 scrollY 也均为 0。按宽度/字号读取的主动滚动状态如下，两个结果状态分别独立验证，并非共用推断：

- 320/390px，15px 字号：scrollY=317；sticky header bottom=58；toolbar top=69.671875；select top=107.109375、height=42；execute top=158.109375、height=42；模式标签 top=120.015625。工具栏距 header 11.671875px，取整滚动与目标 12px 的微差正常。
- 320/390px，18px 字号：scrollY=338；header bottom=58；toolbar top=70.1875；select top=111.328125、height=42；execute top=162.328125、height=42；模式标签 top=122.609375。
- desktop legacy：scrollY=220；header bottom=64；toolbar top=76；select 与 execute top=83、height=42；标签 top=94.28125。
- 所有状态 header top=0。每份 JSON 中模式标签、select、execute 都在视口水平边界内，顶部不低于 header bottom、底部不超过 viewport height；每个控件中心与四个内侧点都命中自身/子元素。9 × 3 × 5 = 135 个采样全部为 `unobscured:true`（其中移动端 120 个）。
- 对照实际 scrolled-viewport PNG，可见上述 toolbar 与 header 间距，未再出现旧元素裁图中 header 压在模式选择器顶部的情况。canonical full-page PNG 同样完整显示工具栏。

### 焦点与自动完成菜单的真实状态

所有 9 份 JSON 的 initial/scrolled/canonical/afterCapture 焦点描述均为 BODY，编辑器 focused=false；代码没有主动 blur/focus/Escape。8 个移动状态的自动完成列表在四种记录中都保持 visible=true、两个选项 `value / values`、选中 `value`；desktop legacy 没有该菜单。实际 PNG 中菜单同样保留，未通过隐藏菜单换取通过。

实际像素中的菜单位置随文档滚动，320px 列表贴到右视口边缘但内容可读，390px 列表在视口内；未遮住模式选择或执行按钮，也未挡住结果。

**小范围诊断记录限制：** 某些 `scrolledState` 或 `canonicalState` 的自动完成菜单 bounding box 是滚动后的立即采样，存在上一帧位置，不能把这些菜单坐标当作与 PNG 同一帧的精确位置。例如 `fixture-sql-refresh-failed-320-font-18-capture-state.json` 的 scrolledState 菜单 y=810.421875，但对应滚动视口 PNG 的菜单位于约 y=472；afterCaptureState 回到 y=810.421875，与规范全页图相符。类似现象出现在 320/15、390/15 刷新失败及 390/18 返回行的部分菜单记录。控件自身坐标/命中及实际 PNG 一致，不影响本次工具栏遮挡闭环；若以后要求菜单逐帧坐标精确，可等待其布局稳定后再记录。这里不声称诊断 JSON 每个异步菜单框均为截图帧精确坐标，也不据此推断产品菜单 bug。

### 新文件定位

根目录为 `/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-c4f4473/`。

四个移动目录分别为：

- `database-query-results-返回行、未知影响及刷新失败在-320px-15px-字号下保持可读-chromium/ui-captures/`
- `database-query-results-返回行、未知影响及刷新失败在-320px-18px-字号下保持可读-chromium/ui-captures/`
- `database-query-results-返回行、未知影响及刷新失败在-390px-15px-字号下保持可读-chromium/ui-captures/`
- `database-query-results-返回行、未知影响及刷新失败在-390px-18px-字号下保持可读-chromium/ui-captures/`

每个目录包含两个状态 `fixture-sql-rows-unknown-W-font-F`、`fixture-sql-refresh-failed-W-font-F`；W/F 为该目录的宽度/字号。每个状态四个文件后缀为 `.png`、`-post-execution-viewport.png`、`-scrolled-viewport.png`、`-capture-state.json`。

桌面目录 `database-query-results-旧响应缺少计数标志时显示未知，自动模式不发送-resultMode-chromium/ui-captures/`，前缀 `fixture-sql-legacy-affected-unknown`，同样四种后缀。

确认图：

- `database-query-results-SQL-确认预览在-320px-大字号下保留注释后的换行-chromium/ui-captures/fixture-sql-confirmation-newline-320-font-18.png`
- `database-query-results-SQL-确认预览在-390px-大字号下保留注释后的换行-chromium/ui-captures/fixture-sql-confirmation-newline-390-font-18.png`

### 本轮最终验收范围

先前「无法从被遮挡元素截图确认移动工具栏完整可见」的证据缺口已关闭。结论基于新视口/规范全页实际像素、真实 header/toolbar 坐标、控制器 hit-test、未削弱的交互与请求计数断言及同 SHA 成功 CI。没有修改产品 CSS，没有必要声称旧图证明的是真实永久不可访问缺陷。

仍不扩展为：真实 NAS/数据库验收、所有滚动位置/全部屏幕/所有状态下无任何问题、所有共享旧危险弹窗的像素回归，或 SQL 图与历史基线逐像素相等。首次报告说明的这些边界继续有效。没有启动本地浏览器/服务器或接触 NAS/生产。
