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

已逐张查看八张原始导出 PNG，未用尺寸代替像素检查。

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


# M15-A 窄屏修复视觉复核（37354be）

## 结果

导出提示横向逃逸与200%根字号每页数量省略已在新制品实际像素中关闭；本提交 CI 仍有一项 SSE 测试失败，不能称完整验收通过。ff92b16 原失败报告与制品保持不变。

## 精确来源

- SHA：37354bed26cd94650f7788070eb18b1079e6eae5。
- Run：https://github.com/Kkwans/nas-control-plane/actions/runs/38411236238。
- 前端 job：115343804947。
- Artifact：https://github.com/Kkwans/nas-control-plane/actions/runs/38411236238/artifacts/11823225927。
- ZIP size：16,231,987 bytes；SHA-256 ca701c2cf18fd7cc3353656b943bf7d7c58667a26ecd4b912a9ebcb1390b244b，与 GitHub metadata 匹配。
- 198 entries：无重复、不安全路径、符号链接；全部 CRC 通过；解压总规模检查通过。

## 实际像素复核

已逐张检查8张 export 原始PNG，宽度分别390/768/1280/1440，每个都有100%和200%根字号。

- 390：100%=390×1274，200%=390×1586；原717px宽度已恢复到390。提示从右侧逃逸列回到按钮下方、工具栏内。完整显示当前页/未加载历史/实时新增不追加/200条与2MiB上限/敏感信息分享前检查。200%内容自然换行，没有重叠或右缘裁切。
- 768：100%=768×1177，200%=768×1327；原1451px宽度已恢复到768。提示在按钮下方跨工具栏一整行，200%换成多行，未溢出。
- 1280：100%=1280×1049，200%=1280×1136；导出按钮、提示及其他控件布局保持完整。200%工具区合理分成两行。
- 1440：100%=1440×1052，200%=1440×1139；没有新增重叠/裁切。
- 四个200%截图均清楚显示“5 条”，没有原先“5…”省略。390px的“每页”标签在选择框上方，实时跟随开关也换行到其标签下方，二者保留独立边界，没有互相压盖。
- export按钮仍为现有次要操作样式，图标和完整条数标签可读。提示为灰蓝辅助文本，无肉眼可见的新对比问题。
- 其他原先200%现象仍存在：部分标题、品牌、时间戳省略，桌面/平板分页按钮文字纵向换行；本次并未将它们宣告为新修复范围。

## 自动化辅助证据与剩余失败

Go、Compose、前端lint/format/typecheck/unit/artifact regression/production build成功。E2E 172 passed / 1 failed。

四宽度导出控件测试通过；新断言同时检查100%和200% body/main scrollWidth <= viewportWidth，并检查每页数量的完整文本及scrollWidth <= clientWidth+1。因此像素结果与几何断言一致。导出键盘焦点、Enter下载、aria-live反馈与scoped axe serious/critical过滤也通过。

唯一失败：log-export.spec.ts:421，实时新记录在下载交接后到达、不改变已冻结文件或重启跟随。可见.el-switch点击已经完成，但streamRequested在5秒内保持false，expect.poll期望true失败。它比原来隐藏input点击超时前进了一步，但仍未进入核心下载冻结断言，不能声称该场景通过。

## 边界

200%仍只是documentElement font-size翻倍，并非浏览器200%zoom；固定px控件字体并未同步翻倍。截图发生在导出按钮focus之前，无导出focus-visible像素；键盘焦点仅有自动化证据。全为固定mock单条系统日志，不等于NAS实机数据、真实SSE传输或实际部署验证。未修改代码、CI、NAS、源文件，也未运行本地浏览器/监听器/PTY；后续新提交与CI由主任务管理。


## 最终浏览器闭环：b5c4844

[CI 38412715113](https://github.com/Kkwans/nas-control-plane/actions/runs/38412715113)确认精确提交b5c4844d257c798e511b5bace8c3af2f33f2039c整体成功，Go、前端及Compose通过，发布跳过。前端job115348938962为51文件610项单元、173/173项Chromium通过。

37354be至b5c4844没有前端产品源码变更，仅修正日志导出E2E的模拟事件源前提；上述八图像素结果仍对应未变化的布局。实时日志事件是内存模拟，原生Blob/anchor触发的下载事件和文件字节由真实Chromium验证，不等于真实SSE网络或NAS采集验收。初次与中间失败记录保留。
