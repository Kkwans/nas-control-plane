# M17 初始化恢复：CI与像素验收

结论：精确提交ab7c1027的隔离CI和六张恢复状态截图验收通过；未发现阻断性裁切或状态误导。此结论不表示真实NAS初始化或账号创建验收。

## 可追溯证据
- 仓库Kkwans/nas-control-plane，分支dot/ncp-modernization。
- SHA：ab7c102741a92a6d100bf9e440c4474e9139e5a5。
- [CI38425178060](https://github.com/Kkwans/nas-control-plane/actions/runs/38425178060)，attempt1；Frontend job115392568891，Go job115392569440，Compose job115392569642。
- 日志确认57文件696单元通过，196/196浏览器通过（3.8分钟），含新增4项bootstrap-recovery测试。前端lint/format/typecheck/web regression/production build及Go tests/vet/native/ARM64 build、Compose config全部成功；release因分支条件跳过。
- [Artifact11828383615](https://github.com/Kkwans/nas-control-plane/actions/runs/38425178060/artifacts/11828383615)：ncp-fixture-ui-ab7c102741a92a6d100bf9e440c4474e9139e5a5-1。
- ZIP23,102,353bytes；SHA2563e7a3a2b7a4eb84ef8ebea22e808f4fe33d23fcd6ff2c16340883dfc6f2d7b4d，与GitHub一致。
- 解压前检查绝对/父目录穿越路径、符号链接、加密、允许PNG/JSON格式、文件数、单文件与总大小、压缩比；全部CRC通过。270文件，总解压25,079,359bytes。逐张PNG摘要见文末。

## 实际测试覆盖
源码frontend/e2e/bootstrap-recovery.spec.ts全部API在浏览器中截获；请求账本只记录方法与路径，不记录密码请求体。
1. bootstrap503部分结果后，GET status确认initialized，进入登录但不创建会话。
2. bootstrap409已初始化后，同样只读复核进入登录。
3. 网络结果不明、复核仍setup，保留初始化界面、清空两个密码、不自动重试。
4. 复核失败进入结果未确认状态；再次检查仅GET status，失败后再恢复登录，不重放bootstrap/login。

测试确认提交与复核期间禁用表单并防重复submit；密码清空、原/system URL保持、无topbar/受保护请求。每个截图场景100/200%检查说明、错误码、操作的真实文字行框及裁切祖先、body无横向溢出；axe无critical/serious问题。该axe门禁不是“所有级别零问题”。

## 六张PNG实际检查
- 409恢复登录：390宽，100/200%根字号。说明“控制台已初始化，请登录。”完整可读，密码框为空、确认密码框消失，登录按钮清楚。200%纵向页面增高但横向无内容缺失。
- 503恢复登录：1440宽，100/200%。卡片居中，说明/表单/操作没有互相覆盖；200%产生正常纵向滚动，fullPage截图包含下面登录按钮，不能误称按钮始终首屏可见。
- 状态未确认：390宽，100/200%。BOOTSTRAP_STATUS_UNCONFIRMED在200%分两行，完整可读；重新检查按钮、说明和标题没有横向裁切。没有密码控件或已登录界面。
- 轻微非阻断语义问题：成功复核后的“控制台已初始化，请登录。”仍使用红色error文案样式。文字内容清楚，可将来改为中性/提示色；不影响恢复流程或本次验收。

这些六张PNG没有capture-state JSON伴随；SHA来源是GitHub run/artifact绑定及其测试源码，几何来自CI内断言，不能宣称逐张JSON/像素对照。没有测试全部状态×全部宽度组合：503是1440，409与未确认是390，网络仍setup未截图。100/200%是根字号缩放，不是真实浏览器zoom。没有创建真实账号、连接NAS或执行部署；账号/密码均为隔离fixture数据。

## 截图尺寸及摘要
- fixture-bootstrap-already-409-390-root-100.png: 390×900, 128446bytes, SHA256 f915313862b57b28cfb0f68e46346799e10640f0c75da1684e9205b316339baa
- fixture-bootstrap-already-409-390-root-200.png: 390×1075, 132933bytes, SHA256 e9dca69531105a89a89363f8cea2d405647d0d6491874ef558c0c6d99ed86c8d
- fixture-bootstrap-partial-503-1440-root-100.png: 1440×900, 514032bytes, SHA256 18c1d5285b1a8a0065709c10a6ab3efa7ecdaaa8d41662038a0bfac4ce06f5d3
- fixture-bootstrap-partial-503-1440-root-200.png: 1440×1239, 700853bytes, SHA256 4db3af9d7af9b57986c84101b09923061385a189582ee3b69843304c305cc210
- fixture-bootstrap-status-unconfirmed-390-root-100.png: 390×844, 146319bytes, SHA256 2501925093d9853897b787837c606629653475f3ca9e424fc174f556358cfe1a
- fixture-bootstrap-status-unconfirmed-390-root-200.png: 390×844, 122671bytes, SHA256 9def66e62a98e1a5d48b524e51835d6ea96a590ddae09d292487d4c568c733fd

## 后续提示色优化（尚待新CI）

ab7c102截图中的红色提示作为原始证据保留。后续只按稳定BOOTSTRAP_RECONCILED码使用中性提示色，保留真正校验/登录/退出错误的危险样式与status/alert语义，不根据文案文本判断；新颜色需要后续提交的真实截图复核。
