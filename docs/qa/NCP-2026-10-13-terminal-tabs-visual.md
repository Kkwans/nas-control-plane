# 终端标签：首次真实CI与像素

`53962e4541d0812266afd2fe6ed566f82ff11a13`，[CI 38454094162](https://github.com/Kkwans/nas-control-plane/actions/runs/38454094162)，2026-10-13 19:30 UTC检查。

## 阻断结论

1226单元 / 69文件通过，**278浏览器通过、7失败（4.6分钟）**。Go115492207220、Compose115492207172成功，前端115492206689失败。

四项为已知设置字号选项不可见。新增三项终端失败为critical `aria-required-children`：全页/terminal可访问性，以及390/1440标签截图用例。`.terminal-tablist[role=tablist]`拥有普通`.terminal-tab-close`按钮，axe明确报告不允许的子项`button[aria-label]`。需修正真实角色结构，不能排除规则。

七项其他新标签行为用例实际通过，原十三项终端用例也通过。两项截图用例在第一份“两标签”截图后的axe断言中止；四标签、关闭确认、退役连接及第五连接等待的后半段未完成，不能算通过。

## 两幅已查看的实际PNG

逐张查看 `terminal-tabs-two-{390,1440}.png`，核对两份geometry capture-state：documentWidth等于390/1440，恰好一个tabpanel可见，隐藏tabpanel均inert。两标签名称、关闭入口、新建按钮和容量边界说明可读，桌面正常排列，390px说明换行完整。

390px full-page图顶部出现“跳到主要内容”链接，固定topbar覆盖页面标题上部，存在焦点/滚动截屏状态需进一步核对；不能把这幅图称作完整首屏布局通过。其余新状态尚无图。

本提交raw axe JSON名为`*-axe.json`，不在工件筛选范围内；违规由准确CI日志证实。后续filename修复3258dff将保留raw axe，不能回溯称本提交已有该文件。

## 工件与边界

官方分片2工件11841354557，13,546,614字节，SHA256 `a40047d77b0bbc7b99fbc1199be695eacedec6b863230aa29108f4bab556f8b0`；165条目 / 15,119,247展开字节。大小/摘要、安全路径、类型、体积/展开比和CRC通过。所需两幅PNG与两份geometry均在此分片。

全部API/WebSocket为fixture，无真实NAS、PTY或Docker exec。JSON不内嵌提交SHA，按准确运行/工件摘要关联；普通字体、未覆盖200%放大或控件中心命中。语义修复后还需新准确CI、全部八幅状态图及保留的axe记录验收。

## tablist语义修正，关闭确认焦点仍阻断（2026-10-13 19:48 UTC）

`96e22ac6bc288bc7fa9a00d822347d5f88919432`，[CI 38456112234](https://github.com/Kkwans/nas-control-plane/actions/runs/38456112234)：1226单元通过，282浏览器通过 / 3失败（5.4分钟）。Go115499146846、Compose115499146700成功；前端115499146988失败。设置六项已全部通过；tablist critical axe已消失。

新的三项终端失败：两种宽度在spec280断言“保留标签”应获焦点，但实际inactive；另一项spec322按Escape后alertdialog仍为1。确认截图在焦点断言后，因此仍没有关闭确认/退役状态图，不把未走到的断言标为通过。

实际逐张查看四幅两/四标签PNG，核对四份geometry及四份保留的raw axe JSON：390/1440宽度均无横向溢出，恰一可见tabpanel，其余inert；raw axe均0违规。四标签窄屏各名称、外置关闭当前标签、新建入口与容量说明均完整，容量满时新建禁用。此前390px跳转链接及标题覆盖现象在这组图中不再出现。

两份工件11844096279/11843811557大小、SHA256与安全ZIP校验见同期设置QA；四幅标签PNG和八份JSON来自准确96提交。关闭确认焦点、Escape及退役/第五连接后半段仍待下一准确运行闭环。
