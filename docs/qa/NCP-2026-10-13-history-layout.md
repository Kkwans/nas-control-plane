# NCP 历史图表布局与移动选择器最终复核

日期：2026-10-13。最终审阅提交：`f9c3fc34dba7a09520098380d264643832b07f40`。

## 最终结论

**本次图例与轴单位布局修复，以及 320/390px 八个长名称传感器的指定交互验收已闭环。** 图例/单位相交、单样本时间标签重复已消失，历史缺口与独立传感器缺读数的呈现保持正确。最终真实 Chromium 用例通过，新增四张下拉/搜索 PNG 均已实际打开检查。

最终 [CI 38387732185](https://github.com/Kkwans/nas-control-plane/actions/runs/38387732185) 中 Frontend、Go、Compose 检查成功；本次独立读取 job 状态及前端日志，确认 355 项前端单元测试、148 项 Chromium 测试通过。ARM64 发布任务跳过，不将其描述为已发布或已部署。此报告只验收监控相关截图和交互；同一 CI 的数据库查询截图由另项复核负责。

## 最终证据与完整性

- [Artifact 11806559065](https://github.com/Kkwans/nas-control-plane/actions/runs/38387732185/artifacts/11806559065)：`ncp-fixture-ui-f9c3fc34dba7a09520098380d264643832b07f40-1`。
- 独立读取元数据确认 run/head SHA，创建 `2026-10-13T11:08:06Z`，到期 `2026-10-27T11:08:04Z`。
- GitHub connector 下载为 Sediment 文件，再经 `download_file` 落地。ZIP 为 11,446,156 bytes，独立 SHA256 精确匹配：`4801d55cbe5b54b88280dce9bda56394a01132cf1f1762371ca203291d08a5c5`。
- 解压前检查无绝对路径、盘符、路径穿越、反斜杠、重复条目、符号链接或非普通文件；检查条目、单文件及总展开大小、CRC，不覆盖旧目录。
- 安全展开至 `ncp-artifacts/ui-f9c3fc3/`：139 文件，135 PNG、4 JSON，展开 11,837,400 bytes。
- 最终 artifact 中监控相关 PNG 共 32 张；四张新增的 320/390 下拉和搜索截图已逐张用原尺寸查看。其余 28 张监控 PNG 以及六张 Overview PNG，与上一轮 `7ad9ae8` 对应图 RGB 完全相同。布局修复后的目标图卡已在上一轮实际查看；不将文件比对冒称为再次逐张人工审阅。

## 最终下拉与搜索的实际像素

四张最终图分别为 `fixture-monitoring-history-320px-eight-long-sensors-dropdown.png`、`fixture-monitoring-history-320px-bounded-sensor-search.png`，以及对应的 390px 文件；尺寸分别为 320×900、390×900。

### 打开八个已选传感器

- 搜索 input 在选择框内出现，有清楚的蓝色焦点边界；其位置没有再被已选标签遮住。
- 两个已选标签与 `+6` 保留在选择框内。下拉面板位于控件下方，左右边界均在窄屏之内。
- 长选项在面板宽度内换行。完整可见的选项保留名称尾部，选中项右侧有勾选状态；图中的 `<img src=x onerror=alert(1)>` 以普通文字出现，没有被渲染成图片。
- 列表位于可滚动区域，顶部部分条目在滚动边界处截断属于当前滚动位置，不是单个选项被固定行高裁掉。新样式允许完整条目自然多行。

### 替换传感器后搜索

- 查询框实际显示 `sensor-63`。两个可见已选标签变为 sensor-01、sensor-02，符合移除 sensor-00 并加入 sensor-08 后的顺序。
- 面板同时保留已选项及匹配项；sensor-63 可见，颜色为禁用态，符合当前仍选满八项的限制。不能因为搜索结果保留已选项，就把它误报为没有过滤。
- 长名称、查询文字、勾选标记及滚动条均在面板内；未见水平溢出、单位/图例互相覆盖或面板挤出屏幕。
- 面板正常覆盖其下方图例的一部分，这是打开下拉的浮层行为；关闭后图表布局已由用例再次检查，不能把打开时的遮挡当作静态排版错误。

## 已通过的交互范围

读取最终提交的用例并与成功 CI 对应，两个宽度均完整执行以下流程：

1. 六类图卡的图例、单位行和 canvas 分区不相交，canvas 高度至少 170px；八条图例都有原始完整 title/可访问名称，未注入 img。
2. 用 Space 隐藏及恢复内存曲线，aria-pressed 与实际 canvas 线条像素同步变化。
3. 普通鼠标点击可见选择器 wrapper，随后确认 input 聚焦、aria-expanded 为 true、不再隐藏，input 中心真实命中它自身。没有使用 force click。
4. 初次展开为 50 个 DOM 选项；未选的第九项禁用。移除一项后可选第九项，再恢复八项并更新图例。
5. 真实按键输入 `sensor-63`，查询值、匹配项及八项上限正确；DOM 仅为八个已选项加一个匹配项，共九项。
6. 第一次 Escape 清空查询，第二次关闭；ArrowDown 重新打开并恢复 50 个候选，八项选择保持不变；再次关闭后页面没有横向溢出。

其中四张 PNG直接证明打开和搜索状态的视觉结果；计数、键盘切换、替换选择、关闭/重开及命中区域等过程由真实浏览器断言证明。两类证据没有互相替代。

## 布局修复的已审像素

上一轮 `7ad9ae8` 已实际打开 20 张目标 PNG：全部 12 张新移动图卡、六张桌面历史正对照/缺口图、单样本监控页及 Overview 部分数据页。这些最终对应图像完全相同。

- 320px 的五张普通图卡为 296×308，390px 为 366×308；八传感器温度卡分别为 296×671、366×671。
- CPU 图把“左轴 · %”和“右轴 · 负载”放在独立一行，左右刻度完整。内存/存储 `%`、网络 `KB/s`、磁盘 I/O `MB/s` 均在图例下方、绘图区上方。
- 八个长名称图例逐行排列、按宽度省略，开头 sensor-00 至 sensor-07 能区分；170px canvas 未被压扁。全文通过可访问名称/title 保留，最终下拉截图补充了可视的多行全文读取证据。
- 桌面内存/网络缺口中间仍没有跨段线或伪零线；温度单个传感器缺失时另一条保持连续。
- 单样本孤立点仍可见，时间刻度改为 `01:59:35`、`02:00:00`、`02:00:15` 等不同秒值，不再重复同一 HH:mm。
- Overview 部分数据页中的 CPU/内存图例与 `%` 已分开，负载单位独立；下方卡片自然下移，没有覆盖。该部分数据 fixture 不扩大为所有实时数据场景的验收。

## 失败到闭环的证据链

### 首轮移动用例停在隐藏 input

`7ad9ae8` 的 [CI 38385550446](https://github.com/Kkwans/nas-control-plane/actions/runs/38385550446) 有 139 项通过、两项新增移动用例失败；该轮 [artifact 11805527380](https://github.com/Kkwans/nas-control-plane/actions/runs/38385550446/artifacts/11805527380) 只有 12 张移动图卡，没有下拉/搜索截图。其 ZIP 10,524,942 bytes，SHA256 为 `ff861aea74639363bb68316acd73ead7e3fa8b74ed9e308cc864e391a5516aea`，曾独立核验并安全展开为 120 PNG、4 JSON。

实际关闭状态图显示两个长标签、`+6` 和箭头，布局正常。安装的 Element Plus 2.14.3 会将 multiple、空查询、未聚焦 input 的 wrapper 标为 `is-hidden`，CSS 使用 opacity 0、负 z-index 和绝对定位。此时点击隐藏 input 会被标签拦截，真实入口应是可见选择器 wrapper。因此测试改为普通 wrapper click，并保留展开后焦点和实际命中区域检查。

### 第二轮暴露冻结时钟与 Vue 冒泡冲突

后续 `d0593a7` 已进入可见 wrapper 点击，但出现“获得焦点却不展开”。安装包源码及独立执行的无浏览器 jsdom 复现确认：

- Vue 3.5.40 给子级处理过的 click 写入 `e._vts = Date.now()`；父级若看到该值小于或等于其监听器 attached 时间，就丢弃该事件。
- fixture 的 `setFixedTime` 让两者始终相等。Element Plus 标签先处理 click，而 wrapper 的原生捕获监听器仍会聚焦 input，于是呈现 focused=true、expanded=false。
- 独立嵌套 Vue 复现中，冻结时间只执行原生捕获和子级；Date.now 前进 1ms 后会执行父级。
- 使用真实安装的 ElSelect/ElOption 再次复现，冻结时间为 focused=true/expanded=false，前进 1ms 后为 focused=true/expanded=true。

最终只去掉监控 fixture 的 `setFixedTime`，保留从固定日期开始自然前进的 `clock.install`、固定样本时间、普通鼠标路径及全部交互断言。没有用强制点击或修改产品 CSS 绕过失败。最终 f9c3fc3 的真实 Chromium 成功运行和四张实际截图补足了这一闭环。

## 仍然适用的边界

本次闭环限于 320/390px、指定静态历史数据和真实 Chromium 流程。移动历史读取错误、清空全部传感器、768/桌面八长名称、全部真实传感器命名、200% zoom、其他浏览器、屏幕阅读器实操和真实 NAS 性能仍未由本轮新增场景覆盖。不能写成所有设备或全部监控状态已经穷尽验证。

相对 `7ad9ae8`，最终 120 张共同 PNG 中 118 张 RGB 相同，只有非目标日志交互视口及用户页有变化；新增 15 张，其中四张属于本次监控下拉/搜索，其余为数据库查询相关。没有扩大为全站 135 PNG 的人工验收。

本次复核只读仓库、使用 connector 下载、离线校验/图像查看及无浏览器 jsdom 复现；没有启动本地浏览器或监听端口、操作 NAS/Agent/Docker、修改产品代码、提交或部署。

## 本地复核文件

- 最终原图：`ncp-artifacts/ui-f9c3fc3/`。
- 最终监控/Overview 文件路径及尺寸：`ncp-artifacts/history-layout-review/image-manifest-final.json`。
- 最终 RGB 比对：`ncp-artifacts/history-layout-review/rgb-comparison-final.json`。
- 首轮布局证据保留在 `ncp-artifacts/ui-7ad9ae8/`；关闭选择器裁剪、首轮清单与比对在 `ncp-artifacts/history-layout-review/`。
- Vue/真实 ElSelect 时钟复现脚本和结果位于 `ncp-artifacts/chart-select-clock/`。

以上是同一工作区 QA 路径；对外下载引用上方带期限的 GitHub artifact，并保留对应 commit/run。初始 `52a7d85` 的缺陷报告仍保留追溯，本报告更新其布局与移动下拉验收状态。
