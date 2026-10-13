# M15-A 日志导出 UI 制品复核（失败基线）

## 结论

ff92b16 未达到交付条件。实际 PNG 确认 390/768px 的导出范围/敏感信息提示逃逸至工具栏右侧，形成页面横向滚动；1280/1440px 的导出按钮和提示正常。修复后必须以新 SHA 的 CI 与制品复核，不能将本报告当作最终验收通过。

## 来源与完整性

- 精确提交：ff92b16ec422d9008312dd4cfac35ca39899e5ed。
- CI：https://github.com/Kkwans/nas-control-plane/actions/runs/38409445080
- 前端 job：115337584718。
- 制品：https://github.com/Kkwans/nas-control-plane/actions/runs/38409445080/artifacts/11820762309
- 制品名：ncp-fixture-ui-ff92b16ec422d9008312dd4cfac35ca39899e5ed-1。
- ZIP：16,099,951 bytes。SHA-256 dd9bdcb6600327c32a5c33e24b50e9bd729d151b5cfecb8a828dd78deb139038，与 GitHub metadata / upload 日志完全一致。
- 196 entries；无重名、绝对路径、父目录穿越、反斜杠路径或符号链接；全部 CRC 检验成功；检查总解压规模后提取。

## CI 精确失败证据

Go checks / Compose config 成功；前端 lint、format、typecheck、unit tests、web artifact regression、production build 成功。E2E 为 170 passed / 3 failed：

1. pages.spec.ts:52，/logs tablet：bodyWidth 1451 > viewportWidth 768。
2. pages.spec.ts:52，/logs mobile：bodyWidth 717 > viewportWidth 390。
3. log-export.spec.ts:419，实时新记录下载交接测试：60 秒超时，.follow-switch.getByRole('switch').click() 命中不可见 input.el-switch__input，持续等待 visible。尚未进入实际 SSE 下载冻结断言，因此不能据此认定产品下载冻结功能失败，也不能声称该场景已通过。

## 像素复核与几何证据

已通过 view_image 查看八张原始导出 PNG，未用尺寸代替像素检查。

- 390px 100%：717×1274；200%：717×1545。提示从约 x377 开始在工具栏右侧单独排成第二列，正常 390px 视口只能看到开头的一小部分。提示包含导出范围与敏感信息警告，因此这是真实信息可见性缺陷。按钮本身在左侧工具栏内完整，可读，底部独占一行；没有按钮文字重叠。
- 768px 100%：1451×1177；200%：1451×1327。同类提示逃逸，从约 x748 开始，几乎全部在视口外。按钮在左侧全宽行，未被裁切。过滤器行被异常撑高，密度明显退化。
- 1280px 100%：1280×1049；200%：1280×1136。按钮位于工具区第二行；提示横跨工具栏底部，字体放大后换成两行，没有互相覆盖或溢出。
- 1440px 100%：1440×1052；200%：1440×1139。同上，提示完整；按钮/图标/条数清楚，白底淡边框符合现有次要操作样式。
- 100% 提示为较小的灰蓝辅助字；肉眼没有发现新的明显低对比不可读文本。四个导出可访问性测试均通过，因此 scoped axe 的 serious/critical 过滤无违规；这不等于所有 WCAG 条款通过。
- 200% 根字号下，分页大小展示为“5…”；侧栏品牌、部分标题说明、时间戳出现省略，上一页/下一页在平板及桌面窄格中纵向换行。导出按钮、Element Plus 选择框/搜索框字号基本没有随根字号翻倍，而说明/标题等 rem 字体翻倍。记录为现有相邻控件的字体放大限制，不将其等同于真实浏览器 200% zoom 验收。

## 启用、禁用、焦点覆盖

- 八张新 export PNG 都是 fixture 单条系统日志、按钮启用状态。截图发生在键盘聚焦之前，无导出按钮的 focus-visible 原始图，不声明完成焦点像素验收。
- 辅助查看当前制品 fixture-logs-time-range-390-scrolled-viewport.png：0 条状态导出按钮清晰呈浅色禁用样式；按钮没有裁切，范围提示及禁用原因仍随右侧逃逸缺陷出屏。此图焦点在时间选择框，有清楚的蓝色轮廓，不能拿来证明导出按钮轮廓。
- 测试源码在第二张截图后进行 button.focus → Tab → Shift+Tab，检查 :focus-visible 与 outline/box-shadow，再通过 Enter 实际导出，检查 aria-live 提示。四宽度测试通过，作为键盘/焦点自动化证据。
- 没有导出中、错误、未应用搜索草稿、请求中等状态的专门截图；相关行为测试通过不替代这些状态的像素验证。

## 前序比较

已查看 ui-00c5f8f 中 fixture-logs-mobile-390x844.png（390×1156）和 fixture-logs-tablet-768x1024.png（768×1077）的实际像素。两者工具栏正常堆叠，页面宽度与视口一致，无右侧提示列。此前没有导出按钮/提示。因此当前横向逃逸为新增回归，而非基线已有。比较数据分别为空日志与当前单条 fixture，不进行逐像素差异阈值判定。

## 截图 helper 与验证边界

log-export.spec.ts:174 的 captureExport 仅在 NCP_CAPTURE_UI=1 时 fullPage + animations disabled，使用 ui-captures/fixture-log-export-{width}px-{100|200}pct.png。四个宽度各设置 1000px 高视口，emulateMedia reducedMotion reduce。200% 是把 document.documentElement font-size 乘 2；没有调用真实浏览器缩放，页面宽度未减半，固定 px 控件未同步放大。

所有新截图使用 mockApi、固定 collectedAt=2026-10-13T00:00:00.000Z、pageSize=5、单条 fixture.service 系统日志。不是 NAS 实机、真实 journald、真实权限/会话或用户浏览器端下载环境。未启动本地浏览器、监听服务、PTY；未连接 NAS；未修改源码、CI 或重试任务。

修复优先级：先处理窄屏 flex-column 与 flex-wrap/100% flex-basis 组合，随后用新制品验证提示回到工具栏下方且全页宽度正常；修正 SSE 测试命中可见开关外壳；若需要真正覆盖200%浏览器缩放，应另加真实缩放/重排验证并补焦点状态图。
