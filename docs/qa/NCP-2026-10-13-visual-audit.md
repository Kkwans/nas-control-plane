# NCP 本轮 CI fixture 截图审查

日期：2026-10-13。基线 commit：`b4b43a9dd8bd76299232dc45ec945b10a8583aef`。CI run：`38369147701`，artifact：`11798771246`。主线程已核验下载包 SHA256：`ff27d18d0cbc5f2d9e6c8766bf4d8f370ca7487c90d8104f9413f9ebd2195c51`；本审查未重复下载或独立核验 ZIP。

截图来源：[本轮 CI 运行](https://github.com/Kkwans/nas-control-plane/actions/runs/38369147701)；[48张PNG压缩包](https://github.com/Kkwans/nas-control-plane/actions/runs/38369147701/artifacts/11798771246)。产物有保留期限；文件索引见文末。

## 结论

保留现有浅蓝白、高密度管理台与 Lucide 线性图标方向。桌面标题、导航、表面层级和状态文字总体一致，不需要整体换皮。先修明确的控件裁切、分页断字和文字截断，再验证手机表格与分区导航，最后收敛空态密度。

最明确的三个视觉缺陷：
1. 390px Docker 页分页“上一页/下一页”逐字竖排。
2. 1280px Docker 表格最右删除按钮被表容器右缘切掉。
3. 数据库详情手机连接说明硬切，连接路径跨整行仍过早省略。

截图是受控 fixture 的真实浏览器输出，不是真实 NAS 数据。监控只有一个样本，空趋势是此状态的证据，不能推断线上图表永久空白。用户管理“0 个账号”也是 fixture 状态，不能据此认定实际 Root 账号丢失。

## 检查方法与覆盖

已读 `docs/design/NCP-DESIGN-SYSTEM.md`、`docs/plans/NCP-2026-10-13-frontend-audit.md`，并保留已有风格约束。本次没有改 UI、启动服务、运行真实 NAS、执行终端或提交管理动作。

- 捕获集：12 路由 × 4 viewport = 48 PNG。
- 实际像素检查：用 view_image 看过 4 张由本次 PNG 制作的联系板，覆盖全部 48 张的结构总览；随后单独打开 32 张原始 PNG：全部12张390手机、全部12张1440桌面、4张768平板、4张1280紧凑桌面。其余16张只有缩略结构检查，不能称逐张精检。
- 长图在查看工具中可能按显示上限缩小，不据此估算实际字号或判定对比度数值。没有做像素级 WCAG 测量、DOM hitbox 测量或 200% zoom。
- 文件名中的390×844等是浏览器viewport，并非PNG物理尺寸。多数为full-page长图，例如手机settings实际390×3011、monitoring390×2304、overview390×2088、system390×2100。不能把整张长图当首屏；超出844px本身也不等于溢出bug。
- 这是默认页面状态的静态审查，不是完整任务流审计。没有验证 hover、Tooltip、Tab/读屏、菜单展开、横向滑动、抽屉、动效、输入法、软键盘、请求时序或真实部署。

## 路由检查步骤与健康度

| 步骤 | 页面 | 可见状态与判断 |
|---|---|---|
| 1 | `/` 总览 | 数据摘要清楚；手机四张指标单列加重复趋势空态使页面很长，辅助字小 |
| 2 | `/sites` | 站点和操作结构明确；手机末项筛选不完整，成功面板保留禁止的左侧强调线 |
| 3 | `/docker` | 桌面列表/手机卡片切换清楚；手机分页断字与1280右操作裁切需要优先修 |
| 4 | `/databases` | 分组、引擎、状态可分辨；手机状态胶囊排入“状态”标签显得拥挤 |
| 5 | `/databases/sqlite-main` | 详情信息完整度需改善；手机连接说明/路径过早截断；桌面数据库导航未高亮 |
| 6 | `/databases/sqlite-main/tables/settings` | 桌面字段对齐清晰；手机固定操作列挤压可读数据区，需验证横滑任务 |
| 7 | `/monitoring` | 时间范围和卡片体系一致；一个样本状态下六张空趋势占满长页，能力不足与等待采样视觉过于相似 |
| 8 | `/logs` | 筛选和空结果下一步明确；桌面时间单位被省略，空表高度偏大 |
| 9 | `/terminal` | 未连接状态、目标、身份、连接动作清楚；两处同主色连接按钮略重复，不能凭图验证PTY |
| 10 | `/system` | 设备、链路和硬件层级明确；手机字段区留白较大，CPU核心数省略，第四个tab初始不可见 |
| 11 | `/users` | 规则与账号两个区块清楚；0账号仍绿盾“可登录”容易误读为健康，属空态文案/色彩问题 |
| 12 | `/settings` | 分区和自动保存状态明确；长页排序列表信息密度较低，桌面字体选项被省略 |

## 发现与优先级

P2 = 影响信息读取、操作辨识或导航定位；P3 = 视觉一致性/密度改善。本次没有截图能证明新的P1数据正确性问题。静态审查中的F01–F05等行为风险仍需独立处理，不能被本报告替代。

### V01 / P2：手机Docker分页逐字断行
- 证据：`docker/mobile`，图片底部约y968–1024。每页标签分两行，上/一/页与下/一/页三字竖排；平板和1440相同页码结构正常。
- 建议：分页按钮不收缩到单字，文本nowrap；不足时将每页选择与翻页组分成两行。保留完整触控区域，不靠缩小文字解决。
- 验收：390宽、长本地化标签、多个页码时文字完整；分页区域不越界；上一页/下一页实际hitbox满足项目44px规范。

### V02 / P2：1280Docker最右操作按钮被裁切
- 证据：`docker/desktop-compact`，右侧表边x≈1251，删除按钮x≈1218起，其右边框与圆角不可见；两行都出现。1440图完整。
- 建议：为操作组保留宽度，先收缩路径列或提前切换响应式布局；不要仅用overflow:hidden压住整体宽度。
- 验收：1280和相邻断点各操作的完整边界可见，按钮bounding box均落在可见容器内；不只验body scrollWidth。

### V03 / P2：数据库详情连接说明与路径过早裁切
- 证据：`databases-sqlite-main/mobile`，连接状态说明在第一列末端硬切，完整文本在1440图为“已读取对象目录，可打开数据表或视图。”；路径所在最后一格已经跨整行，却约在`/volume2/Project/nas-co…`便省略，右侧大面积空白。768图也过早省略路径。
- 建议：状态解释允许两行；位置使用实际可用宽度、适当断词或可展开/复制完整值。浏览器title不是手机可用的完整值方案。
- 验收：390、768可完整读取状态句，路径不在有空余宽度时省略；长路径可通过明确方式读全。
- 可疑实现定位，仅供修复导航：当前工作树DatabaseDetailView的source-summary dd/small使用nowrap+overflow:hidden；status dd又是grid。当前代码已可能超前于截图commit，不能把成因当作已浏览器验证。

### V04 / P2：深层数据库页面缺少模块导航选中态
- 证据：1440数据库列表左侧“数据库”有浅蓝选中块；详情和表页左侧该入口没有选中块，面包屑仍明确属于数据库。
- 建议：模块选中态按所属模块而非只匹配列表URL；保留现在对称圆角浅蓝选中样式。
- 验收：列表、来源详情、表页均高亮数据库；aria-current语义另测。对应静态审查F11的可见部分已得到截图支持。

### V05 / P2待交互验证：手机表格有效数据视窗过窄
- 证据：表页390图，行号、id和约156px固定“编辑/删除”操作列占据绝大部分宽度，key只露少量字符，value不在初始可见范围。页面确实写有“左右滑动查看字段，编号和行操作已固定”。
- 结论边界：存在可见性成本；不能宣称字段无法访问，也不能仅据图要求推翻表格。
- 先验证：手势/键盘滚至key、value，固定列不遮挡焦点、编辑对象和值能同时辨认。若失败，再考虑收窄编号/id或以行详情收纳次级操作，保留安全操作对象上下文。

### V06 / P2待交互验证：手机tab/筛选末项隐藏缺少明确提示
- 证据：sites390最后“已忽略”计数被截断；数据库表390仅露出“SQL”一角，完整桌面有SQL定义、执行SQL两项；system390初始只见前三个分区，桌面第四项为“服务与能力”。
- 建议：先验证容器横向滚动、键盘焦点跟随和选中项自动可见。必要时增加边缘渐隐/可见滚动提示或窄屏适配，不能直接判功能不存在。

### V07 / P2：桌面日志时间选择省略关键单位
- 证据：logs1440与1280时间选择显示“最近6…”；同状态768与390显示“最近6小时”。
- 建议：为短时间文案保留合理min-width，优先收缩搜索框或换行。时/天单位属于筛选意义，不能被默认截掉。
- 验收：各时间预设不丢单位，200%文字仍可确认当前选择。

### V08 / P3：空态占位和手机指标密度过低
- 证据：overview390实际2088px，四张单列指标后接三张相似“趋势数据未就绪”；monitoring390实际2304px，六张大空图；logs390实际1156px，空结果说明下仍有很长纯白表身。system390系统字段每项占较大垂直距离，settings390长至3011px。
- 建议：总览可评估手机2×2指标；共享采样等待摘要、各指标保留简洁状态；无能力的I/O/温度采用稳定不可用提示，与“收到下一样本开始绘制”区分；空表在无数据时设置合理内容高度。先建立有数据/空/错误对照，避免缩空态导致加载布局跳变。
- 不要：伪造曲线、统一把所有卡片压小、把单样本fixture当线上采集失败。

### V09 / P3：普通辅助文字太弱，多个按钮视觉尺寸小于项目目标
- 证据：总览指标容量/核数、趋势等待说明、数据库状态说明、Docker工作路径为很小的灰蓝字；数据库表“编辑/删除”视觉按钮约30px高，站点/Docker图标按钮视觉盒约40px，排序箭头按钮亦小。
- 建议：优先把有决策作用的辅助字提升到项目允许的可读基线，减少冗词而非缩字号；小图标可保留，扩大实际触控区域。
- 边界：截图只能量可见盒，不能证明DOM点击区同样小；对比度与实际computed字号未测，不能宣布WCAG失败。禁用按钮低对比与可用文字须分别评估。

### V10 / P3：局部样式违反现有语义规范
- 证据：sites各视口“自动识别已完成”面板左侧绿色单边强调条，与设计系统禁止左侧单边强调冲突；docker“需关注”使用红色，已停止使用橙色，而规范给关注/警告橙、错误红、离线灰；数据库列表手机状态胶囊中混入“状态”标签，显得像小卡片。
- 建议：成功面板改为完整细边/图标+文字；先确认需关注是错误还是降级，再绑定正确tone；状态标签与pill分离。不要更换既有Lucide图标或用emoji填补。

### V11 / P3：空账号状态用绿盾“0个账号可登录”造成混合信号
- 证据：users390与1440均为0账号，但摘要仍绿字绿盾，同时空态提示创建第一个账号，说明又写至少一个可用账号。
- 建议：空/零可用时用中性或需关注语义，文案明确“暂无可用账号”；不推断真实Root会话损坏。

### V12 / P3：层级与文案小修
- 证据：settings桌面“中文字体”当前选项省略；system手机CPU“4物理/…”隐藏逻辑核数；database详情表卡“数据表”标识重复；设置排序列表中文名下再显示overview/sites等内部标识；terminal上下两处同色“连接终端”；若干页大标题的中文/拉丁粗细观感不一。
- 建议：优先关键值完整，内部标识仅在必要时展示；终端空态保留一个主要操作、另处降为次级；统一标题字号/字重token并在真实字体加载后验收，不凭图片断言实际使用某字体。均低于裁切/信息缺失优先级。

## M11/M12建议边界

- M11可先处理小而确定的V01/V02/V03/V04/V07，补390/768/1280/1440本轮同fixture前后截图和元素可见性断言。不要顺便改业务能力或权限。
- M12先做V05/V06交互证据，再处理空/错/缓存刷新/两样本以上趋势、最大字体和键盘。视觉密度调整应以有数据对照验收，保留浅色高密度风格。
- 真实NAS、动效、键盘/读屏、交互抽屉、危险确认、字体加载失败、真实API不在这48图证明范围内，须另有证据。

## 实际原图清单与截图索引

以下链接指向本地证据，相对本报告可打开；用于内部复核，不是已向外发布的文件。`原图`表示单独使用view_image打开；`联系板`仅表示结构总览看过。

### desktop-wide

- [fixture-databases-sqlite-main-tables-settings-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-ma-7e337-gs-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-tables-settings-desktop-wide-1440x900.png) · 1440×957 · 原图
- [fixture-databases-sqlite-main-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-main-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-desktop-wide-1440x900.png) · 1440×900 · 原图
- [fixture-databases-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--databases-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-databases-desktop-wide-1440x900.png) · 1440×900 · 原图
- [fixture-docker-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--docker-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-docker-desktop-wide-1440x900.png) · 1440×900 · 原图
- [fixture-logs-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--logs-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-logs-desktop-wide-1440x900.png) · 1440×978 · 原图
- [fixture-monitoring-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--monitoring-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-monitoring-desktop-wide-1440x900.png) · 1440×955 · 原图
- [fixture-settings-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--settings-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-settings-desktop-wide-1440x900.png) · 1440×1767 · 原图
- [fixture-sites-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--sites-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-sites-desktop-wide-1440x900.png) · 1440×900 · 原图
- [fixture-system-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--system-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-system-desktop-wide-1440x900.png) · 1440×1015 · 原图
- [fixture-terminal-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--terminal-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-terminal-desktop-wide-1440x900.png) · 1440×908 · 原图
- [fixture-users-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--users-在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-users-desktop-wide-1440x900.png) · 1440×900 · 原图
- [fixture-overview-desktop-wide-1440x900.png](ncp-artifacts/ui-b4b43a9/pages--在-desktop-wide-可进入且无横向失控-chromium/ui-captures/fixture-overview-desktop-wide-1440x900.png) · 1440×1023 · 原图

### desktop-compact

- [fixture-databases-sqlite-main-tables-settings-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-ma-988f3-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-tables-settings-desktop-compact-1280x800.png) · 1280×892 · 原图
- [fixture-databases-sqlite-main-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-main-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-desktop-compact-1280x800.png) · 1280×800 · 联系板
- [fixture-databases-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--databases-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-databases-desktop-compact-1280x800.png) · 1280×800 · 联系板
- [fixture-docker-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--docker-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-docker-desktop-compact-1280x800.png) · 1280×800 · 原图
- [fixture-logs-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--logs-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-logs-desktop-compact-1280x800.png) · 1280×975 · 原图
- [fixture-monitoring-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--monitoring-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-monitoring-desktop-compact-1280x800.png) · 1280×952 · 联系板
- [fixture-settings-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--settings-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-settings-desktop-compact-1280x800.png) · 1280×1783 · 原图
- [fixture-sites-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--sites-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-sites-desktop-compact-1280x800.png) · 1280×800 · 联系板
- [fixture-system-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--system-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-system-desktop-compact-1280x800.png) · 1280×1012 · 联系板
- [fixture-terminal-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--terminal-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-terminal-desktop-compact-1280x800.png) · 1280×903 · 联系板
- [fixture-users-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--users-在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-users-desktop-compact-1280x800.png) · 1280×800 · 联系板
- [fixture-overview-desktop-compact-1280x800.png](ncp-artifacts/ui-b4b43a9/pages--在-desktop-compact-可进入且无横向失控-chromium/ui-captures/fixture-overview-desktop-compact-1280x800.png) · 1280×1020 · 联系板

### tablet

- [fixture-databases-sqlite-main-tables-settings-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-ma-7c1d1-settings-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-tables-settings-tablet-768x1024.png) · 768×1149 · 原图
- [fixture-databases-sqlite-main-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-main-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-tablet-768x1024.png) · 768×1024 · 原图
- [fixture-databases-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--databases-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-databases-tablet-768x1024.png) · 768×1024 · 联系板
- [fixture-docker-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--docker-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-docker-tablet-768x1024.png) · 768×1027 · 原图
- [fixture-logs-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--logs-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-logs-tablet-768x1024.png) · 768×1077 · 原图
- [fixture-monitoring-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--monitoring-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-monitoring-tablet-768x1024.png) · 768×1390 · 联系板
- [fixture-settings-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--settings-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-settings-tablet-768x1024.png) · 768×2189 · 联系板
- [fixture-sites-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--sites-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-sites-tablet-768x1024.png) · 768×1024 · 联系板
- [fixture-system-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--system-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-system-tablet-768x1024.png) · 768×1329 · 联系板
- [fixture-terminal-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--terminal-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-terminal-tablet-768x1024.png) · 768×1073 · 联系板
- [fixture-users-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--users-在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-users-tablet-768x1024.png) · 768×1024 · 联系板
- [fixture-overview-tablet-768x1024.png](ncp-artifacts/ui-b4b43a9/pages--在-tablet-可进入且无横向失控-chromium/ui-captures/fixture-overview-tablet-768x1024.png) · 768×1569 · 联系板

### mobile

- [fixture-databases-sqlite-main-tables-settings-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-ma-dc5f9-settings-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-tables-settings-mobile-390x844.png) · 390×954 · 原图
- [fixture-databases-sqlite-main-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--databases-sqlite-main-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-databases-sqlite-main-mobile-390x844.png) · 390×844 · 原图
- [fixture-databases-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--databases-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-databases-mobile-390x844.png) · 390×844 · 原图
- [fixture-docker-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--docker-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-docker-mobile-390x844.png) · 390×1069 · 原图
- [fixture-logs-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--logs-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-logs-mobile-390x844.png) · 390×1156 · 原图
- [fixture-monitoring-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--monitoring-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-monitoring-mobile-390x844.png) · 390×2304 · 原图
- [fixture-settings-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--settings-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-settings-mobile-390x844.png) · 390×3011 · 原图
- [fixture-sites-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--sites-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-sites-mobile-390x844.png) · 390×953 · 原图
- [fixture-system-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--system-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-system-mobile-390x844.png) · 390×2100 · 原图
- [fixture-terminal-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--terminal-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-terminal-mobile-390x844.png) · 390×968 · 原图
- [fixture-users-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--users-在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-users-mobile-390x844.png) · 390×1065 · 原图
- [fixture-overview-mobile-390x844.png](ncp-artifacts/ui-b4b43a9/pages--在-mobile-可进入且无横向失控-chromium/ui-captures/fixture-overview-mobile-390x844.png) · 390×2088 · 原图

本报告完成后没有修改任何UI源文件；仅新增本仓库外报告及临时联系板。
