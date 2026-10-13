# M14-C SQL 字节预算：独立实际 CI 截图复核

复核日期：2026-10-13 UTC。基于源码、GitHub CI与下载产物的独立复核。

## 结论

已实际打开并查看该批全部 47 张 SQL PNG，独立校验 15 份 capture-state JSON。新增字节前缀、首行超预算空前缀及 result_too_large 的 320/390px、18px 字号共 18 张图片均已查看；同时复核 29 张旧 SQL 移动/桌面/确认框回归图。

字节预算、空前缀、写入可能已生效等关键安全语义显示正确，警告自然换行且无水平裁切；15 个记录状态的 225 个模式标签/选择器/执行按钮采样点全部命中。发现一项实际状态文字问题：错误结果头仍显示“等待执行”，与下方“执行失败”冲突。故不能把本批报告概括为无发现的全面通过。后续b967c53已提交标题修复，修复后实际证据见末节；本报告只证明原 SHA 的实际状态，不能追溯视为修复后通过。

## 确切来源与 CI

- 仓库：Kkwans/nas-control-plane；分支 dot/ncp-modernization。
- 产品/截图提交：41d8ac3a98bf410d00de3446554a12fcc438164b。
- CI：https://github.com/Kkwans/nas-control-plane/actions/runs/38397786863 ，run number 172，attempt 1，push 事件；开始 2026-10-13T12:30:27Z，最终 updated_at 12:34:23Z；独立读取 run 确认 completed / success。
- Frontend checks job 115297326685 成功：lint、format check、类型检查、生产构建、web artifact regression，以及 45 文件 / 445 项单测和 154 项 E2E 通过。SQL 查询单测文件 DatabaseTableView.query.test.ts 为 44 项；ConfirmDangerDialog.test.ts 为 5 项。
- Go checks job 115297326642 成功，Test、Vet、native 和 ARM64 build 均成功；Compose config job 115297326229 成功。Publish ARM64 release 跳过，没有发布或部署结论。
- Artifact：https://github.com/Kkwans/nas-control-plane/actions/runs/38397786863/artifacts/11813002090
- ID 11813002090，name ncp-fixture-ui-41d8ac3a98bf410d00de3446554a12fcc438164b-1；创建 12:34:20Z；到期 2026-10-27T12:34:19Z。
- GitHub 元数据与上传日志均报告 14,925,685 bytes；SHA-256 5aa5e778059d34055b2f6a2f8e60f07c0389ee2f32cf6b52b42b7356657a39d4。下载后的本地字节数、SHA-256 均完全匹配。
- 校验后解包新目录：/workspace/scratch/a2c31a226a28/ncp-artifacts/ui-41d8ac3/。

## ZIP 安全及清单

在解包前验证：绝对路径、父目录穿越、反斜线/冒号、重复名称、Unix 符号链接均不存在；文件类型限 PNG/JSON；CRC 全部通过；条目数小于 1,000、单文件小于 20 MiB、单条压缩比小于 500、展开总大小小于 100 MiB。目标目录原先不存在。

全包 190 文件：171 PNG、19 JSON；展开 16,310,373 bytes。SQL 子集 62 文件：47 PNG、15 JSON。

全部 SQL 文件的确切绝对路径、文件大小、SHA-256 与 PNG 尺寸写入：
/workspace/scratch/a2c31a226a28/ncp-artifacts/sql-review-41d8ac3-manifest.json

几何校验摘要写入：
/workspace/scratch/a2c31a226a28/ncp-artifacts/sql-review-41d8ac3-geometry.json

## 实际像素发现

### 新预算场景

每个宽度 320/390px 均查看 byte-prefix、byte-empty、result-too-large 三个状态各自的执行后视口、主动滚动视口、规范全页图，共 18 张。

1. byte-prefix：橙色块明确“受 1 MiB 结果字节预算限制”“仅展示前 1 行完整结果，单元内容未被裁剪”，并明确截断不限制写入范围，也不代表未生效或回滚。结果头显示返回 1 行 / 返回结果已截断；未知影响行数单独显示。文字在两种宽度完整换行。
2. byte-empty：结果头为“结果行未能展示”；橙色警告为“没有可展示的完整行，不代表查询无匹配”；表格保留 id/amount 列头，空状态为“完整结果行未能展示。”，没有误报返回 0 行、影响 0 行、查询无匹配或写入回滚。
3. result-too-large：红色正文显示“SQL 结果超过 1 MiB 响应预算，无法展示”，建议先核对数据库状态再决定是否重试，读取时减少列或预览大字段。代码 result_too_large 可读，没有误报 Agent 断连或 SQL 未执行。此前橙色截断警告已清除。
4. 500 行与 1 MiB：工具栏显示“最多 500 行 / 1 MiB”，说明区再次区分展示限额与写入范围。本批新增 PNG 实际覆盖 byte_limit；row_limit 专用“达到 500 行展示上限”由源码及已通过的参数化单测证明，不冒充已看过 row_limit 专用截图。

### 发现 V1：错误后头部“等待执行”与正文矛盾

证据：fixture-sql-result-too-large-320-font-18.png 和 fixture-sql-result-too-large-390-font-18.png，以及对应 scrolled-viewport 图。结果头右侧是“等待执行”，下方正文是“执行失败”，且明确写操作可能生效。

源码原逻辑只在没有 queryResult 时以 queryPending 区分“正在执行”/“等待执行”，未考虑 queryError。建议错误状态优先显示执行失败/结果错误。此为低严重度但真实、可复现的状态文案缺陷，关键安全警告仍正确。修复应由新 SHA、测试及新实际截图关闭，不能改写这批 PNG 的结论。

### 限制 V2：完整数据保留不等于每个字符在窄列内可见

byte-prefix 的 ID 9007199254740993 在 390px 图可完整看到，在 320px 图使用省略号；高精度 amount 1.234567890123456789002500 在两种宽度均视觉省略。E2E 的 DOM 文本断言和单元测试验证值仍完整保留；源码 ElTableColumn 开启 show-overflow-tooltip。此次没有实际操作或捕获 tooltip，因此不宣称完整数值已通过悬浮/触摸视觉验收，也不把 CSS 省略号误判为服务端剪裁数据。

### 原 SQL 回归

- 全部 320/390px × 15/18px rows-unknown、refresh-failed 两状态，三种捕获，共 24 张：模式标签、选择器和执行按钮完整；未知影响行数与 fixture-updated 保留；刷新失败明确“SQL 已执行”，提示重读表数据、勿为刷新重跑 SQL。
- 桌面 legacy 三图：自动模式、成功和未知影响行数正确，没有错误声称影响 7 行。
- 两张 320/390px、18px 确认框：注释后的第二条 DELETE 在下一行；80 字符 token 自然换行；取消、确认按钮完整可见。
- 390px 的横向 tab 条左侧可见部分前一个 tab 文本，属于当前横向滚动位置；没有将其扩展为所有 tab 导航可达性验收。

## 坐标、命中和捕获状态

15 JSON 均独立读取：所有 initial、canonical、afterCapture 的 scrollY=0；主动滚动态 topbar.top=0。每个状态三个控件各五个内侧点，225/225 unobscured=true，其中新预算场景 90/90；所有控件 visible，顶部在 header.bottom 下方，底部在 900px 视口内，水平边界在相应视口内。

- 全部 18px 移动状态：scrollY=338；header bottom=58；toolbar top=70.1875；select top=111.328125；execute top=162.328125。
- 全部 15px 移动状态：scrollY=317；header bottom=58；toolbar top=69.671875；select top=107.109375；execute top=158.109375。
- 桌面 legacy：scrollY=220；header bottom=64；toolbar top=76；select/execute top=83。

真实 PNG 与工具栏几何相符，没有重现旧截图中的 sticky-header 遮住模式选择器顶部。

所有 15 状态四次记录的焦点均为 BODY。六个预算状态与四个 390px 旧移动状态保留可见 autocomplete；四个 320px 旧移动状态及桌面 legacy 无菜单。捕获源码未主动 blur/focus/Escape 或隐藏元素。菜单在 budget 图中贴右边缘，但不盖住模式控件、执行按钮或结果警告。菜单位置可能存在滚动后异步布局；不将即时 JSON 的所有菜单坐标当成 PNG 同帧精确证据。

结果区低于 900px 视口底部是页面自然延伸。规范全页图证明内容完整，但执行后的原始视口看不到预算错误正文；本次没有证明自动滚动到错误、读屏播报或所有视口状态的可达性。

## 交互证据与边界

同 SHA E2E 源码保留真实请求计数：预算场景三次点击对应三次 query；前两次成功返回后刷新表行，第三次超预算错误不再刷新；按钮恢复 enabled；没有自动重放。分页连续两次超预算后仍请求同一 offset，不跳过失败页，成功最后一页后可回到 offset 0。完整 CI E2E 成功证明这些断言通过；分页用例没有专用截图，不能称作分页像素验收。

本任务没有仓库写入、产品修改、git 提交/推送、部署、NAS/生产/真实数据库访问、浏览器启动或监听端口。这里只验证 mock fixture UI、CI 结果与其产物，未扩展为真实数据库大结果性能、驱动资源上限、所有屏幕/滚动位置/浏览器/辅助功能全面验收，也没有与历史基线作逐像素相等比较。

## b967c53 修复后定向实际产物复核（2026-10-13 12:45 UTC）

### 新结论

V1 已在 **b967c53e5fd203b686f82b7bd1ca79f476485a16** 的实际 CI PNG 中关闭：320/390px 两种宽度的规范全页图与主动滚动图，结果头右侧均已改为“执行失败”，与下方错误正文一致；写入可能已生效、先核对状态再决定重试等指导保持完整。此结论不改变上文 41d8ac3 原始发现，不借用后续 88f36ef 提交的 CI。

### 修复产物来源

- exact-SHA CI：https://github.com/Kkwans/nas-control-plane/actions/runs/38398946480 ，attempt 1，completed / success，最终 updated_at 2026-10-13T12:42:44Z。
- Frontend job 115301289141：45 文件 / 446 单元测试、154 E2E 通过；SQL 查询单测现为 45 项，共享确认框仍为 5 项。Go job 115301289569 与 Compose job 115301289566 成功，发布跳过。
- artifact：https://github.com/Kkwans/nas-control-plane/actions/runs/38398946480/artifacts/11813168482
- 名称 ncp-fixture-ui-b967c53e5fd203b686f82b7bd1ca79f476485a16-1，ID 11813168482，创建 12:42:40Z，过期 2026-10-27T12:42:39Z。
- 14,950,835 bytes；SHA-256 c0670bcf23beed05953b97a98a34235e17948960c1a7d6a7a01a22a282a8c7cf。下载本地字节数和 digest 均匹配。
- 完成与原批相同的路径、重复名、符号链接、类型、条目数、单文件大小、压缩比、总展开大小及 CRC 验证后，解包到原先不存在的 /workspace/scratch/a2c31a226a28/ncp-artifacts/ui-b967c53/。
- 新包仍为 190 文件，展开 16,342,339 bytes；SQL 仍为 47 PNG、15 JSON。
- 47 张 SQL PNG 路径、尺寸、SHA-256、对原图 RGB 差异边界清单：/workspace/scratch/a2c31a226a28/ncp-artifacts/sql-review-b967c53-manifest.json。

### 实际打开与 RGB 对比

实际打开全部六张 result-too-large 新 PNG，包括两个执行后视口（错误区仍在视口外）、两个主动滚动视口与两个规范全页图；再打开其余 16 张有 RGB 变化的 SQL 图，总计实际查看新图 22 张。所有 47 张新 SQL 图均与同名 41d8ac3 图作 RGB 比对：26 张完全相等，21 张有像素差异。

- 两个 canonical 错误图的差异仅在结果头文字：320px 边界 (247,1035)-(295,1047)；390px (317,1011)-(365,1023)。两张 scrolled 图对应边界为 (247,697)-(295,709) 与 (317,673)-(365,685)。错误正文、布局与其他像素完全相等。
- 320px 错误 post-execution 图只有模式选择器箭头附近 2×1px 的差异；390px 错误 post-execution 图完全相等。它们不能单独证明错误头，因为头部在视口下方。
- 非错误差异主要为 320px、15/18px 旧结果场景实际 autocomplete 菜单出现，以及个别边缘/选择器处少量像素；实际打开确认菜单未遮住工具栏或结果。旧回归 refresh-failed 的警告语义与结果头保持正确，390px refresh-failed 三种图均与原图 RGB 完全相等。
- 没有发现额外独立的 SQL queryError 截图类别；refresh-failed 属于 SQL 已执行后的表刷新警告，不能混同 queryError。本轮不声称覆盖所有 SQL 错误代码的像素状态。

独立重验全部 15 JSON：模式标签、模式选择器与执行按钮均在 header 下方、视口内，225/225 hit-test 通过。未见该窄修复引入布局或控制可达性回归。数值省略号、tooltip 未实际交互、mock fixture 与真实数据库边界等原限制继续适用。仍未编辑仓库、启动浏览器或访问生产。
