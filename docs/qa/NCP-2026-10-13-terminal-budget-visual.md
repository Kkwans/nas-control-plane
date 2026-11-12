# 终端前端预算：CI与过载提示像素

准确提交 `63623f11c2ec37288655b3a11cadfc69c68c8225`，[CI 38450711748](https://github.com/Kkwans/nas-control-plane/actions/runs/38450711748)，检查于2026-10-13 19:06 UTC。

## 结果

**67文件 / 1198单元通过；271浏览器通过、5失败（8.2分钟）**。Go115480577469、Compose115480576993成功，前端115480577076失败。五项失败全部为已知设置fixture选项不可见超时，见设置QA；不能将整体CI标绿。终端原六项及新增七项均实际通过。

新增终端覆盖：390/1440单帧超过16KiB关闭；同task待处理输出精确256KiB允许、再多一字节关闭且不自动重连；真实xterm解析完成释放预算；UTF-8粘贴含包装32KiB边界；bufferedAmount加本次输入64KiB边界；旧owner排队输出与迟到回调不消耗新owner预算。

## 两幅实际截图和状态

逐张查看 `fixture-terminal-output-overflow-{390,1440}.png`。完整显示“终端输出处理过载，正在关闭本地连接，后续输出未保留。”，无省略号、遮挡或目标标签挤压。关闭中状态、禁用的关闭操作及目标切换与用例状态一致；xterm和待确认粘贴已清理。

两份capture-state JSON实际核对：documentWidth等于viewportWidth，说明只有一行且所有Range行框在说明边界内，各目标主/副标签各一行，控件边界不相交且位于工具栏内，axe违规为空。图片为fullPage；几何视口高1000px。JSON仅含geometry/accessibility，未内嵌sourceSha，来源由准确运行、工件ID和摘要关联，不声称有中心命中或200%字体覆盖。

## 工件与边界

官方连接器取得分片2，工件 **11840706529**，**11,555,151字节**，SHA256 **`4ef8bea275ac4f4200efaeeec36a1d9c59bf4125011c8c8731cae0ee9bdddb81`**。135条目 / 12,982,265字节展开体积；大小/摘要、安全路径、文件类型、单文件/总量/展开比和CRC验证通过。所需两幅新PNG及两份JSON均在此分片，不需要下载其他分片重复旧图。

所有Socket/API为隔离fixture，native WebSocket防线记录为空；xterm parser是真实浏览器组件，但没有真实PTY、Docker exec、NAS或远端进程。预算验收不代表服务端吞吐或实际网络压力测试。整体绿色CI仍等待设置fixture后续修正。
