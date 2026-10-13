# 2026-10-13 改进验证记录

## 基线

- 基线5b81557，独立分支dot/ncp-modernization。
- 前端117单测、lint、typecheck、build通过；controlstore/compose/database/docker测试通过。
- 计划及开发分支CI提交f451fe2；对应run38368227961已启动，结果待确认。
- 本地浏览器两次启动受socket权限限制；没有本轮UI截图通过结论。

## M01 Job快照隔离

- 对registry锁内对象深复制Layers及CompletedAt，覆盖创建、查询、列表、取消成功/失败、删除成功/回滚和持久化入口。
- 保留存储IO在锁外，不改变API、不声称修复持久化乱序或取消状态机。
- 聚焦 `go test ./internal/httpapi -run 'Test(JobSnapshot|JobRegistry|NormalizeJobSnapshot)' -count=1` 通过。
- 相同筛选 `go test -race ... -count=10` 通过；使用可控fake并发，不连接Agent/Docker/PTY。
- 下一阶段M06仍需解决旧写覆盖终态、删除后迟到UPSERT以及取消确认。

## M02 图标响应隔离

- 本地上传图标、同主机代理图标、默认图标统一发送 CSP `default-src 'none'; style-src 'unsafe-inline'; sandbox` 与 nosniff。
- 修复直接导航上传SVG文档时缺失隔离策略的分支；保留图片字节及现有缓存机制。外部资源依赖SVG可能受策略限制，旧浏览器缓存不会立即更新。
- 聚焦 `go test ./internal/httpapi -run 'Test(LocalSiteIcon|FallbackSiteIcon|ProxySiteIcon|SiteIconProxy)' -count=1` 通过。
- 覆盖本地GET/304/Range、handler级HEAD、proxy SVG/PNG、fallback；HEAD不代表路由已支持HEAD。验证策略头、空body与Range字节，无真实站点访问。
- 独立审查无阻塞项；真实浏览器脚本禁止执行/图像显示仍未验收，不称为净化SVG内容。
- 初始分支CI f451fe2/run38368227961全通过；M01提交192fed6/run38368650031启动，结果待确认。

## M04 数据库详情请求与凭据隔离

- 来源切换、卸载、会话撤销时清空敏感草稿并取消观察；发现、自动连接、手动连接与诊断绑定目标和请求代次。
- Store连接过程只在连接及目录读取均成功、请求仍有效时一起发布冻结凭据与目录；不提前缓存未验证的新凭据。
- 首次发现有加载/失败重试/不存在分态；首次目录读取失败提供独立“重新读取目录”，连接错误不会误显示目录重试。
- 13项聚焦测试通过，包含实际点击发现重试、目录重试并恢复；全量当前前端145测试、typecheck、lint通过。
- 独立审查指出的目录重试缺口已补，未执行真实数据库或NAS操作；浏览器端到端仍待CI。

## 已确认CI

- f451fe2：38368227961 成功。
- 192fed6：38368650031 成功。
- 32b2a47：38368942080 成功。
- b4b43a9：38369147701 截图任务已启动，尚未检查产物。

## M03 Compose编辑事务与确认

- 抽取工作区session，冻结保存内容；保存期间继续编辑保持dirty，跨项目/文件/关闭重开/注销/卸载隔离旧结果。
- 校验绑定文本修订，确认使用同一冻结payload；重复提交持锁。关闭已提交Job仅停止订阅，不谎报后端取消。
- 只有completed终态报告部署成功；部署成功更新实际基线，不重新读取覆盖后续编辑。
- 16项单测通过，全前端145测试、typecheck/lint通过。新增2条受控延迟浏览器回归已通过测试发现，尚待远程执行；迟到校验使用JSON消费后的事件循环屏障，不用任意sleep。
- 独立审查无新增P0/P1，未操作真实Compose项目。

## M05 容器详情与日志查询隔离

- 容器详情/日志共享一个目标请求作用域，切换、关闭、卸载或会话撤销后旧结果失效。
- 日志查询拥有独立分页busy、历史请求和SSE代次；筛选切换重置分页状态；合并按来源/资源/流/id保持身份，不让旧历史覆盖较新的实时记录。
- 新增9项单元测试及2条fixture浏览器回归；当前全量154项前端测试、typecheck、lint、生产build通过。
- 浏览器测试仅发现解析通过，实际新增交互待CI。
- b4b43a9/run38369147701与c2ee5ba/run38369388207均成功；48张本轮fixture截图已下载并核验artifact摘要，正在逐图审查，不能概括为全部视觉验收。

## M07 Compose恢复第一切片

- 回滚保留context values，但脱离原请求取消/超时，使用独立两分钟context预算；这不是同步磁盘IO或不响应取消runner的硬退出保证。
- 同Manager、同规范目标路径的校验/备份/部署/回滚串行化，等待可取消，空闲锁回收。
- CreateTemp独占创建0600唯一备份并Sync，固定时钟同秒不覆盖；部署与回滚错误均可追溯。
- compose race×30、vet及agentsocket/httpapi的Compose聚焦测试通过；root另跑race×3通过。测试仅临时目录与fake，不执行Docker。
- 独立审查无阻塞；修正20ms墙钟脆弱测试，用runner进入屏障取消、synctest虚拟截止时间及已取消ctx/空闲锁竞争回归。
- 两包完整测试尝试被Unix socket权限限制阻塞，不称全量通过。基线hash、跨进程/软链接别名/lifecycle协调、结构化恢复状态及健康验收仍属后续工作，M07不标全完成。

## 08:40 UTC CI 闭环与视觉证据

- f06479ffa9cca04b911fbf68f01fe6c9ac33fb07：[CI 38370586768](https://github.com/Kkwans/nas-control-plane/actions/runs/38370586768) 全部必需检查成功；154项单元测试、76项浏览器测试、lint/typecheck/build及Go测试/vet/本机和ARM64构建、Compose验证通过。发布相关任务按条件跳过。
- 该后代验证覆盖M03/M05/M07首切片，修正的是可见选择器点击；没有跳过失败测试。
- [48张fixture截图审查](NCP-2026-10-13-visual-audit.md)确认分页、操作列、摘要及时间单位问题；不等同于完整交互/读屏/NAS验收。

## M12 可读性首切片

- 分页按钮至少44px且不拆字；Docker footer换行并解除嵌套同名样式叠加，操作列不再以超出容器的最小列宽布局。
- 数据库说明与路径自然断行；日志时间选项保留单位空间。仅修改组件/页面CSS，保持已有交互逻辑。
- 新增12项真实文字与元素边界Playwright用例，覆盖320/390/768/1280/1440相关视口；尚待CI运行和after截图检查。
- 本地154项原有单元、typecheck、lint、生产构建通过；随后M11增加6项组件回归，合并检查单独记录。

## M11 移动导航与模块活跃态

- 00c5f8f：关闭态inert，打开后焦点循环/背景隔离，Escape/遮罩/路由恢复；实时断点及卸载清理；窄高滚动与数据库三级路由模块标注。
- 新增6项组件与5项真实键盘浏览器测试；独立只读审查无阻塞。root最终全量160项测试、typecheck、lint、生产构建通过。浏览器尚待CI，不称读屏实测。
- b208e40与00c5f8f均已远端确认，分别触发CI38371621964、38371717985，08:48 UTC仍在运行。

## 08:55 UTC 后续闭环

- b208e40/38371621964 与00c5f8f/38371717985成功；12项新增布局、5项导航浏览器测试已由CI执行。四类可读性问题的[修复后截图复核](NCP-2026-10-13-visual-verification.md)通过，数据库子路由视觉选中恢复。
- 8d75639/38372051121终端输入取消首片CI通过。fake输入泵覆盖满缓冲、正常转发、错误与超时；独立审查无阻塞。尚未覆盖完整handler清理顺序的独立fake测试，容器同步写可能仍阻塞，会话撤销传播另行实施。
- 2bb32b3存储首片：版本CAS/仅创建/条件删除与可重复迁移，controlstore race及HTTP Job兼容通过，双连接竞争20轮通过。旧运行时仍用兼容Upsert，本片不宣称B05已修复。
- 补测旧接口INSERT revision=1及UPDATE保留取消/错误元数据，race通过。

## 09:32 UTC 可靠性与导航闭环

- db959e4总览收藏四态、08b1b96安全stale socket恢复及168855f Job运行时/协议均已获得各自完整CI成功。M09实际运行的是普通Linux CI隔离Unix socket与受控异常退出，不是NAS部署。
- ca3aa18增加不安全HTTP方法来源检查、端口保留代理契约与稳定403映射；Go CI通过，首次整体CI受已有导航焦点测试失败影响。
- 7060732两项Job浏览器测试通过；96通过/1失败的真实失败是窄高导航从769px回768px时CSS先隐藏焦点元素、MQL回调后到。9bf1de9修复焦点历史与过时断点事件，增加6项组件回归，保留原浏览器断言。
- [9bf1de9 CI 38376241497](https://github.com/Kkwans/nas-control-plane/actions/runs/38376241497)全部必需检查成功，闭环上述导航失败及来源防护、Job浏览器验证。未跳过失败测试。
- 41c9d5c辅助文字提升：数据库摘要与移动Docker元数据字号下限12px并随实际偏好缩放；路径换行。全前端194项单元、typecheck/lint/build通过；新增30项视口×字号浏览器用例已发现，执行和新截图尚待CI。
- 来源防护尚未进行真实nginx/双站点浏览器攻击或NAS实机测试；Go原生Origin回退比较authority而非scheme，详细边界见来源方案。

## 09:40 UTC 独立历史采集与辅助字号

- [41c9d5c CI 38377160024](https://github.com/Kkwans/nas-control-plane/actions/runs/38377160024)成功：194项单元测试、127项浏览器测试（含30项新字号矩阵）、Go/Compose检查通过。90张fixture PNG已下载并核验SHA256；辅助文字截图视觉复核单独记录，不能仅以断言通过替代视觉验收。
- e776c17已推送：M13-A服务生命周期采集、主样本/温度原子写、独立7天清理及HTTP/SSE零历史写入。两个独立后端审查无阻塞；四包TestMetrics race×3、完整controlstore race及go vet ./...通过；补测同分钟候选错误恢复和待写失败保持，OpenAPI检查通过。[CI 38377913634](https://github.com/Kkwans/nas-control-plane/actions/runs/38377913634)全部必需检查成功。
- A仍保留HTTP/SSE直接summary读取；多标签消重、过期提示与流预算属于正在实施的B1。有界历史查询与完整图表缺口为B2，Docker对账为C。所有测试仅fake/临时SQLite，未采集真实主机或NAS指标。

- [辅助文字截图复核](NCP-2026-10-13-secondary-text.md)：30张目标截图通过；共同基线47张RGB不变、12张预期变化，另1张日志滚动后全页捕获出现悬浮页头/skip-link异常，已安排视口断言与规范全页重拍，尚未关闭该异常。

## 10:05 UTC 精度、共享实时与截图闭环

- [9bcd424 CI 38379521199](https://github.com/Kkwans/nas-control-plane/actions/runs/38379521199)成功。数据库直接标量values/keys精度修复由HTTP/protobuf/临时SQLite回归验证，原代码负对照可重现相邻大主键舍入；不保证客户端已经舍入的JS Number、嵌套对象或数据库自身任意精度。
- [6ca7d3b CI 38380342147](https://github.com/Kkwans/nas-control-plane/actions/runs/38380342147)成功。后端共享快照、连接/逐帧写预算、心跳及状态分离通过独立race十轮；正常Linux CI执行完整Go测试，包括实际loopback HTTP空闲6秒后再次发送和provider关闭EOF。全部使用fake源；Docker仍逐连接RPC，未宣称C已完成。
- [37ed104 CI 38379835528](https://github.com/Kkwans/nas-control-plane/actions/runs/38379835528)与[实际截图/JSON复核](NCP-2026-10-13-logs-capture.md)关闭日志捕获异常验收：390px明确scrollY=120、header y=0、未聚焦skip-link在视口外，视口及回顶全页像素均正常。旧异常具体成因仍为推测。
- B1前端独立审查修复了遗漏storage质量判断、fixture时钟漂移、EventSource未就绪注入，以及移动端隐藏状态提示等问题。最终228项单元、typecheck/lint/build通过；5项新浏览器场景与新状态截图待CI。


## 10:27 UTC 历史查询边界与图表缺口

- B1前端11d04d3的228项单元及5项新增实时新鲜度浏览器场景通过；该次完整CI失败于既有视觉测试暂停页面时钟后axe异步检查超时。f03775f将原严重/关键无障碍断言分离到正常推进时钟的独立测试，不删除断言、不放宽阈值。[CI 38382227692](https://github.com/Kkwans/nas-control-plane/actions/runs/38382227692)完整通过。
- e213199历史查询后端：精确闭区间、同一只读快照、父样本与温度行双限额、8MiB完整JSON含换行限额、5秒协作取消预算；过大/无效/超时返回稳定错误，无部分数组。有效7天10081样本×6短名传感器返回5,050,583字节；7传感器超过65536温度行时明确拒绝。独立审查与race/取消/并发快照/全Unicode计数测试通过，[CI 38382724643](https://github.com/Kkwans/nas-control-plane/actions/runs/38382724643)完整成功。非驱动峰值内存硬上限或NAS性能实测。
- 52a7d85历史图表：真实时间轴、每个缺口一个null、计数器重置/不安全整数不算速率；稀疏温度索引、最多八条选中曲线、有限搜索DOM；加载/错误/空数据与成功状态互斥。独立审查无阻塞，320项单元、typecheck/lint/build及六项新Chromium场景发现通过。像素测试有连续曲线正对照、保留两端要求和缺口无连线断言；[CI 38383466014](https://github.com/Kkwans/nas-control-plane/actions/runs/38383466014)完整通过，139项Chromium测试含六项新增历史图场景实际执行通过；截图独立视觉复核中，不等同于NAS实机验收。
- M07外部修改回滚保护与明确恢复失败代码正在独立实施；不与上述已发布历史查询/图表修改混合提交。


## 10:40 UTC Compose 恢复与视觉后续

- [540e532 CI 38384406312](https://github.com/Kkwans/nas-control-plane/actions/runs/38384406312)全部必需检查成功。回滚前紧邻Rename比较原始字节，外部变更/删除/非普通文件/不可读取时跳过覆盖与重应用；通过严格RPC白名单和Job持久化区分恢复阶段结果。双轮独立race验证及321项前端单元、typecheck/lint/build/OpenAPI通过。bufconn与临时SQLite覆盖超时、取消和迟到结果，未运行真实Docker/NAS。
- 这只是尽力缩小检查至写入窗口，不是跨进程原子CAS；新编辑基线和成功后外部修改检测未完成。重应用命令成功也不证明容器健康。
- [历史图截图复核](NCP-2026-10-13-history-visual.md)实际打开16张监控PNG：缺口/独立传感器/错误/空态通过，但发现共享图表既有图例与单位重叠、单点分钟刻度重复，且缺长名称/八选中移动截图。已继续实施排版与相应覆盖，未将CI绿色等同于全部视觉通过。
- M14-B1显式结果模式正在按更新后的数据库方案实现；保留Root SQL能力，不重复执行写语句，旧Agent须在执行前拒绝不支持的显式模式。


## 11:12 UTC SQL 结果模式、真实交互与剩余截图检查

- 76a461c交付M14-B1：显式结果模式与可选QueryWithOptions、已知/未知影响行数、行集关闭检查、成功后不重放的刷新、冻结SQL/mode与保留换行的确认预览。核心、RPC/HTTP和UI独立复核通过；355项前端单元与Go隔离race/build/vet/OpenAPI通过。
- 76a461c首轮CI有146项浏览器通过、2项继承的监控多选测试失败。f9c3fc3未删除断言，移除监控fixture固定Date.now；独立Vue运行时及真实Element Plus DOM复现证明冻结时间令标签事件与父invoker时间戳相等，导致点击冒泡被丢弃。修正保留固定历史数据，允许UI时钟推进。
- [f9c3fc3 CI 38387732185](https://github.com/Kkwans/nas-control-plane/actions/runs/38387732185)完整通过：355项单元、148项Chromium，以及Go与Compose检查。两项移动传感器下拉/搜索测试和七项SQL截图场景已实际执行。发布任务保持跳过。
- 监控最终artifact中的四张320/390px下拉/搜索图已实际打开：长名称在限定宽度内换行、原始标签文字未解释为HTML、选中标记和第八项上限可见；既有28张监控与6张总览图与上一已审查轮RGB一致。
- SQL结果、刷新失败提示、影响行数未知和确认换行在实际图中可读，但部分工作台元素截图被sticky面包屑覆盖顶部；现有产物缺滚动坐标，尚不能断定为产品控件不可达。正在规范化捕获并补视口/坐标/命中证据，不以CI通过替代视觉闭环。


## 11:38 UTC 编辑基线与 SQL 视觉闭环

- 47c078c已推送：Compose磁盘原始hash、必填基线、checked-only RPC、锁内两次写前核对和命令成功后核对；界面复读目标与冻结提交相等才推进hash，保留后续dirty编辑。三层独立审查通过；审查补齐revision存储期间取消检查及两种持久化交错，已提交revision不伪称撤销。
- [47c078c CI 38390439962](https://github.com/Kkwans/nas-control-plane/actions/runs/38390439962)全部必需检查成功：420项前端单元、151项Chromium，Go及Compose检查通过。新兼容/冲突验证使用fake/tempfile/SQLite/bufconn，实际Compose E2E为浏览器mock API。未执行真实NAS部署。
- [c4f4473 CI 38389527278](https://github.com/Kkwans/nas-control-plane/actions/runs/38389527278)通过，SQL截图规范化未改产品CSS/API。实际打开29张SQL图并核对9份JSON，135个工具栏命中点全部有效；控件在sticky页头下方、回顶全页与拍后scrollY为0。先前截图验收缺口在这些状态下关闭，[完整视觉证据](NCP-2026-10-13-sql-visual.md)保留成因边界。
- 少数即时采样的补全菜单坐标落后滚动一帧，实际PNG位置正确；不把该非阻塞坐标限制隐藏成全部动态坐标稳定。
- 下一片M08-B正在实现活动流会话租约、提交后撤销/到期、WS连接关闭和logs/jobs SSE写入预算；不宣称全部容器exec/PTY子进程已回收。


## 12:10 UTC 活动流撤销闭环与后续切片

- 7dc9d21已推送：活动流以数据库重验的会话租约准入，退出仅撤销当前token，改密/禁用/删除在提交后撤销该用户活动流；到期与父请求取消也清理。WS同时关闭浏览器和独占Agent连接，logs/jobs SSE补齐逐帧写入预算。三个独立审查通过，真实SQLite会话至四类流的隔离回归及race通过。
- [7dc9d21 CI 38393981077](https://github.com/Kkwans/nas-control-plane/actions/runs/38393981077)全部必需检查成功：420项前端单元、151项Chromium，Go测试/vet及本机/ARM64构建、Compose配置检查通过；发布任务跳过。
- 撤销保证限定在同一Service实例；Login/UpdatePassword最终状态重验不检测期间禁用再启用的ABA。关闭流传输不等同于容器exec或PTY全部子孙已结束。
- M08-C1已进入实现：已取得容器attachment后的可中断I/O、总关闭预算和退出未确认分类；预会话握手、全局启动锁、主机PTY另片。M14-C同时实现保守结果字节计费、完整行前缀截断和RPC/HTTP编码边界，尚未形成完成或测试通过结论。


## 12:23 UTC 容器终端有界关闭

- M08-C1收紧为同一net.Conn与缓冲Reader的attachment契约，取消直接关闭连接；Write/Resize感知生命周期，Close总预算从入口开始。传输关闭、exec主进程退出核验、本地I/O收尾分别判断，无法确认时保存独立错误；日志不输出底层敏感错误。关闭中保留Manager配额。
- 独立审查用纯fake负对照复现了首帧Send阻塞取消、Close先于本地Read收尾的缺口。最终采用唯一输出sender负责started及后续帧，等待已准入Read与输入gate使用剩余关闭预算，耗尽时明确本地清理未确认；不把返回RPC之前强制join依赖RPC返回的transport泵当作正确方案。
- 最终28项新cleanup测试与4项M08-B回归，race重复20轮通过；独立复核race重复10轮通过。三包仅编译、terminal/agentsocket vet、server/agent构建及精确diff检查通过。实际连接用内存net.Pipe，Agent使用受控fake Session，退出超时/错误通过fake gateway注入；没有真实Docker/PTY/NAS操作。
- 预会话ExecAttach握手、Manager.Start全局持锁、宿主机PTY和任意子孙进程清理仍不在此片。两秒Close预算加100ms最后通知及调度开销不能表述为严格2.000秒RPC保证；预算耗尽不能表述为全部本地owner已结束。


## 12:27 UTC 数据库结果字节预算

- M14-C核心、RPC/HTTP、UI/OpenAPI共21个文件冻结。1MiB保守结果计费与最终编码复核、Scanner复制前拒绝超大值、完整行前缀截断、固定offset整页失败、AsMap前节点/深度预算与稳定result_too_large已实现；不提高gRPC默认上限、不重放SQL。
- 两路独立审查补齐自定义编码/匿名字段及旧protobuf展开边界，以及翻页、排序、页大小失败后的导航跳行问题；最终均无阻塞。首行超限明确提示不是无匹配或未写入，UI保留完整单元且写入错误先提示核对状态。
- 完整database与OpenAPI包、agentsocket/httpapi数据库精确选择、三包相关race、四包vet及native/linux-arm64双命令构建通过。完整前端445测试、typecheck、lint、build/bundle预算和E2E源码检查通过；新浏览器用例与截图待分支CI。
- 最终验证中一次过宽的agentsocket/httpapi测试选择触发既有Unix socket集成，bind被环境拒绝；没有创建listener、没有提权或绕过。该次整包测试明确不算通过，完整集成由授权普通Linux CI另验。测试没有访问用户电脑、NAS或真实生产数据库。


## 12:41 UTC SQL结果验收与Docker共享采集

- [41d8ac3 CI 38397786863](https://github.com/Kkwans/nas-control-plane/actions/runs/38397786863)完整通过：445项前端单元、154项Chromium及Go/构建/Compose检查。独立实际复核47张SQL PNG、15份JSON与225个控件命中点；预算/空前缀/写入风险提示可读。发现失败头仍写等待执行，b967c53已独立修复并补状态迁移测试，446项本地单元及类型/构建/lint通过，新CI待验收。
- [6cb1688 CI 38397142273](https://github.com/Kkwans/nas-control-plane/actions/runs/38397142273)完整通过，闭环容器终端C1隔离验证。保留预握手、主机PTY和后代进程边界。
- M13-C0十个代码/测试文件冻结，独立复核无阻塞；Docker SSE按需单worker、5秒最小启动间隔及RPC预算、深复制/容量/双年龄状态、最后需求取消和旧代结果拒绝。SSE只读缓存不等待Docker，GET仍新鲜直读且可与SSE并行；事件重连对账尚未交付。
- 四包精确TestDockerRealtime测试及race三轮、独立race与六项既有纯fake SSE名额/慢读/心跳/写预算回归通过；全仓仅编译、受影响包vet、OpenAPI与diff检查通过。内存RPC检查1MiB JSON/3MiB protobuf完整清单预算，不改变全局gRPC上限；无真实Docker/NAS采集。


## 12:43 UTC 图标隔离验证补齐

- M02增加真实上传/读取handler的共享契约回归，核对恶意SVG与PNG原字节、内容类型、CSP和nosniff等响应；使用recorder/tempdir，不启动HTTP服务。
- 三项Chromium用例分别验证独立文档脚本/onload阻止、iframe不透明源隔离及SVG/PNG解码与绿色像素兼容；同一未保护SVG正对照必须实际执行。Go独立固定CSP断言防止fixture与handler一同弱化。
- 精确Go测试、ESLint、严格TypeScript及Playwright三项发现、独立审查和diff检查通过；审查删去了图片宿主上无意义的空属性断言。真实浏览器执行仍待分支CI，不将路由fixture重放描述为真实后端网络或NAS部署验证。


## 12:49 UTC CI与视觉闭环

- [b967c53 CI 38398946480](https://github.com/Kkwans/nas-control-plane/actions/runs/38398946480)完整通过：446项单元、154项Chromium。实际复核六张超限错误截图，四张能显示结果头的图仅标题文字像素改变为执行失败；独立校验225个控件命中点，未发现该修复的布局回归，完整证据见[预算视觉复核](NCP-2026-10-13-budget-visual.md)。
- [88f36ef CI 38399324201](https://github.com/Kkwans/nas-control-plane/actions/runs/38399324201)完整通过，C0共享Docker SSE已闭环隔离验证；C1强刷新屏障另片实施中。
- [ad412d8 CI 38399545409](https://github.com/Kkwans/nas-control-plane/actions/runs/38399545409)完整通过：446项单元、157项Chromium，含三项新增图标安全测试。未保护SVG正对照、受保护独立文档/iframe脚本与同源隔离、SVG/PNG像素兼容已在真实Chromium执行；网络响应为与真实handler回归锁定的fixture，不冒充实际部署验证。


## 12:54 UTC 强刷新屏障与动态效果偏好

- M13-C1共11个代码/测试文件冻结并完成分层独立审查：采集开始序号屏障、64等待者、共享串行化与取消、12秒HTTP等待，以及五个既有操作后调用的强制刷新。旧在途采集/Promise不得冒充操作后状态核对，过时请求不覆盖新状态但仍向自己的调用者准确返回结果。
- collector race十轮、HTTP精确选择race二十轮与独立混合SSE/三个GET集成race五轮、八个既有sites及三个dashboard回归race十轮通过；48项store测试、110项相关前端测试、类型/lint/build、Go仅编译/build/vet和OpenAPI解析通过。完整分支CI尚待验收。
- /readyz保留独立3秒探测，不纳入最长12秒强刷新屏障，仍可与共享源重叠；C2事件重连对账尚未完成。此片只保证规定共享读取路径的序列化，非整个进程所有Docker调用唯一。
- 80cb4bc已独立推送：图表动画与xterm光标闪烁实时响应减少动态效果偏好，监听随组件销毁，重连读取当前偏好且不额外创建终端。27项聚焦单测与独立复核、类型/lint/build通过；mock ECharts/xterm测试不等于真实渲染验收。共享工作树469项前端测试同时包含C1，不能算作该独立提交的精确测试总数。


## 13:10 UTC 事件断线对账

- [80cb4bc CI 38400736140](https://github.com/Kkwans/nas-control-plane/actions/runs/38400736140)完整通过，454项单元、157项Chromium；[7855d23 CI 38400983759](https://github.com/Kkwans/nas-control-plane/actions/runs/38400983759)完整通过，469项单元、157项Chromium及Go/构建/Compose，闭环C1强刷新屏障。
- C2共11个代码/测试文件冻结，两层独立审查无阻塞。首订阅与重连全量对账、epoch/需求代次保护、事件dirty合并、1/2/4/8/16/30秒退避及5秒周期真实采集已实现；旧Agent不支持新RPC明确不可用，需要配套升级。
- 根审查发现Agent同步Send可能挡住provider EOF，已改为唯一sender与主生命周期分离；实际fake负对照证明在释放阻塞transport前handler可返回并释放Watch名额。reader协作取消后join，transport sender不能在RPC返回前强制join。
- 核心fake race三轮、Agent独立fake/bufconn race二十轮、五包TestDockerRealtime组合race及编译/vet检查通过。1KiB帧限额、旧Agent协议不匹配、无ready超时、SDK Err/EOF、64需求、重连删除、storm合并和取消代次均覆盖；完整CI待验收。无真实Docker/NAS操作。


## 13:15 UTC JSON请求类型契约

- M10-B二十个代码/测试/规格文件冻结并独立审查；35个JSON路由在四个decoder校验单一application/json，六个auth/user错误出口统一415及requestId。保留Origin/认证/依赖预检、UseNumber、11个无body动作和multipart上传。可选inspect按实际空体判断并保留首字节及原Close。
- 根独立TestJSON race通过（3.422s），审查独立全矩阵与OpenAPI通过；35路由缺失/plain/重复头拒绝与正确MIME错误JSON回归，no-side-effect计数、敏感信息不回显、真实临时SQLite CLI生命周期均覆盖。四处decoder去掉guard及恢复旧ContentLength判断的五个overlay负对照全部按预期失败，未改工作树产品文件。
- 现有37项审计筛选Go回归、六个前端API文件121项测试、新文件lint及Vue类型检查通过；前端契约实际调用35个API函数、11个无body动作与FormData，415无自动重试或清除会话。完整分支CI待验收。
- CLI仍无需Origin，但JSON必须显式Content-Type；原无头或curl默认表单JSON有意变为415。LimitReader隐藏限额后尾部的问题另片，不混称已修复。没有更改真实安全设置、凭据或生产环境。


## 13:26 UTC 完整请求体限额

- [f2580b7 CI 38403123167](https://github.com/Kkwans/nas-control-plane/actions/runs/38403123167)完整通过，469项单元与157项Chromium；[4ddda99 CI 38403759891](https://github.com/Kkwans/nas-control-plane/actions/runs/38403759891)完整通过，521项单元与157项Chromium，Go/构建/Compose均通过。
- M10-C九个文件冻结且独立审查无发现：四decoder先读取完整有界body，再严格解码；保持8KiB/16KiB/1MiB容量，完整空白与尾部计入，35路由超限统一413，保留原400/415及认证来源优先级和数字精度。
- 新增测试覆盖读取边界/分块、读取错误、取消/空读、35路由无副作用、inspect前缀与Close及保护顺序。旧四decoder的overlay负对照产生432项预期失败，证明隐藏尾部等缺陷可被捕捉。冻结后race通过20.041s，安全HTTP回归2.095s、go build ./...、OpenAPI、87项前端契约测试与vue-tsc、diff检查通过。完整CI待验收。
- 不启动本地网络服务、不无限drain、不修改HTTP timeout；取消依赖底层Reader配合。限额是payload完整字节预算，不是进程总内存硬上限。无NAS、真实Docker或生产操作。

## 13:36 UTC 完整请求体限额 CI 闭环

- 208bc58262445180cbea87ea2ae91b2b70e08575 已推送并核对远端；[CI 38405801295](https://github.com/Kkwans/nas-control-plane/actions/runs/38405801295)完整通过：556项前端单元、157项Chromium，Go检查与Compose均成功，发布任务跳过。
- 主机PTY H1正独立审查：已发现并修正初版在直属child自然退出后立即关闭master导致尾部输出丢失的问题；尾部排空与迟到父取消的并发回归仍在补充。不把未冻结工作树或未执行的真实PTY用例计为已验收。

## 13:38 UTC 主机 PTY 取消与回收

- H1改用一次非阻塞RawConn系统调用与可取消gate；Resize不再通过File.Fd恢复阻塞。Write部分成功不重放，共享预算；Close保存首次结果并分别报告本地清理/直属child回收未确认。
- 独立审查发现自然Wait后立即abort会丢失尾部输出，已改为排空立即可读内容，在EOF/EAGAIN结束；自然退出后的输入拒绝不破坏尾部。父取消watcher保留到排空/关闭，覆盖未开始Read时迟到取消；deadline与receipt同时就绪时再次核对凭据。
- 独立纯fake `TestHostCleanup|TestHostRawTransport` race十轮通过；实现方生命周期race十轮及Agent分类测试通过，全包仅编译检查、Darwin/amd64 Agent包交叉编译通过。无本地真实PTY、Shell或子进程用例执行。
- 新增隔离CI用例验证O_NONBLOCK、真实PTY读取消/写背压和零/非零/信号终止直属child回收，尚待完整CI。启动前locale/能力探测、Manager.Open持锁和容器attach握手不在本片范围；保持既有readiness流程，不额外添加无截止的等待。

## 13:44 UTC 主机 PTY 隔离 CI 闭环

- 96070cd00476e26bbd9ac8b69f236033b67f7e7e 已推送并核对远端；[CI 38406804798](https://github.com/Kkwans/nas-control-plane/actions/runs/38406804798)的Go、前端及Compose均通过，发布任务跳过。
- 隔离Linux完整go test执行terminal包3.165s成功，包含保留的交互PTY及新增O_NONBLOCK、空读取消、写背压和直属child零/非零/信号回收测试；本地未执行这些真实内核/子进程用例。此证据不等于NAS实机验证或任意进程树清理保证。
- H2开启预留与短锁发布正在实现，独立审查同步进行；不因等待CI停止后续授权开发。

## 13:48 UTC 开启预留与并发发布

- H2四个代码/测试文件冻结：Manager.Open的Start/Metadata/拒绝后Close移出全局锁，opening/live/closing原子计费；晚到取消不发布，Close返回前不释放资源名额。默认上限及协议不变。
- Agent初始失败也记录安全清理分类，不泄露未发布ID、底层错误或用户输入。独立审查补上裸主错误与带码Close错误组合的分类回归，次级取消/目标错误不再改变原Internal映射。
- 实现方组合纯fake选择器race二十轮、独立审查同一选择器race五轮通过；覆盖TestTerminalStartReservation、既有TestManager、关闭预留与启动中取消回归。根Go构建、冻结hash和diff检查通过。完整CI待验收。
- 无本地PTY/子进程/监听器或NAS操作；不合作的Start/Metadata仍占预留，单次启动期限、SDK握手与任意第三方实现panic不由该片保证。

## 13:55 UTC 开启预留 CI 与日志导出进展

- 037f91f306d6fea7f6acbd971b8a25b455de2529已推送并核对远端；[CI 38408120860](https://github.com/Kkwans/nas-control-plane/actions/runs/38408120860)完整通过，556项前端单元/157项Chromium，Go/Compose均成功，发布跳过。
- M15-A当前页导出代码及纯前端验证正在收尾；独立审查覆盖2MiB准确UTF-8边界、控制字符可逆转义、字段白名单、旧会话与未应用查询禁用、URL清理，发现同步click触发销毁后重访问anchor的边界并已修复。实际下载与布局截图仍待新增隔离浏览器用例执行。
- OpenAPI仅纠正原有“全部日志已脱敏”的过度描述：系统/Agent只覆盖已识别模式，容器不保证脱敏。无接口或权限变更；OpenAPI测试通过0.559s。

## 13:57 UTC 日志当前页导出待验收

- M15-A七个前端文件冻结，新增当前页明确范围/条数的UTF-8日志快照下载、200条/2MiB硬限额、字段白名单、控制字符转义、未应用筛选/旧会话禁用与对象URL释放；导出不新增API请求或自动分享。
- 最终全量前端51文件610项单元通过，lint、类型、format与生产构建通过，最大bundle518.0KiB低于540KiB预算；独立审查早一轮606项通过后，补充了四项单元回归。最终fixture改动后lint/diff及独立E2E类型检查通过。
- 新增16项Playwright用例覆盖真实下载事件与文件字节、当前页/第二页、UTF-8/时间精度、资源释放、搜索草稿、来源代次、SSE快照、退出/导航、键盘及四宽度100%/200%截图和axe。尚未在本地执行真实浏览器；CI与实际截图复核仍是未完成门槛。

## 14:10 UTC 日志导出浏览器发现回归

- ff92b16的[CI 38409445080](https://github.com/Kkwans/nas-control-plane/actions/runs/38409445080)前端E2E为170通过/3失败；Go、Compose与前端其他阶段通过。两个既有页面回归测试发现390/768视口body宽717/1451，实际八张新截图确认说明文字逃逸为工具栏右侧第二列。
- 根因是横向wrap规则延续到窄屏column布局及说明的100% flex-basis。修复显式关闭column wrapping、重置说明basis，并让分页/跟随控件在放大字体下换行；页大小选择器改为随rem增宽，避免“5…”裁切。
- 第三个失败是新增SSE快照用例点击隐藏Element Plus input；改为真实可见switch包装元素，不使用force。原有overflow断言不变；新增导出用例补充100%/200% body/main宽度及分页文字不裁切断言。
- 独立源码审查、59项聚焦测试、lint/类型与fixture类型检查通过，替换CI与实际像素复核尚待执行。初次失败事实及制品hash见[日志导出视觉复核](NCP-2026-10-13-log-export-visual.md)，不得追溯改写为通过。

## 14:12 UTC locale 探测资源边界

- H3四文件冻结并独立审查：仅locale探测继承父context，750ms预算、100ms WaitDelay及32KiB固定stdout；stderr丢弃，完整成功且精确命中才选C.UTF-8，否则C，不缓存。
- 精确纯fake/data/config选择器二十轮15.115s、race一轮1.781s通过，独立相同选择器也通过；全包仅编译、vet及本机/ARM64命令构建通过。实际locale/helper/PTY/监听器未在本地运行。
- CI helper覆盖成功/失败/超限/父取消及管道未关闭；继承管道模拟由两个测试直属child持有真实exec stdout-copy管道，各自唯一Wait owner和有界失败清理，不能据此宣称生产回收任意后代。取消测试记录Wait后的context错误，防止把探测deadline误作显式父取消通过。
- WaitDelay不抢占Process.Wait或PATH/启动中的内核等待，H2预留不会因未知owner提前释放；Bash能力CombinedOutput仍为独立缺口。
- 日志窄屏修复37354be已推送并核对远端，[替换CI 38411236238](https://github.com/Kkwans/nas-control-plane/actions/runs/38411236238)进行中，未将初次失败改写为通过。

## 14:20 UTC 日志视觉闭环与实时测试修正

- 37354be替换CI为172通过/1失败；实际8张PNG确认390/768恢复视口宽度，导出说明完整回到按钮下方，四宽度200%根字号“5 条”不再省略。原始失败及新制品hash分别保留在[视觉复核](NCP-2026-10-13-log-export-visual.md)，不是浏览器真实zoom验收。
- 剩余失败的精确原因：共享installMockApi将EventSource替换为不发HTTP的内存事件源，测试等待HTTP路由永远不会发生。单文件修正改为页面初始化后观察当前事件源，检查实际开关aria状态、URL参数及单实例，再保留真实Blob下载字节、点击后实时条目显示、文件快照不变和流未重启断言；不将模拟SSE描述为真实网络传输。
- 610项单元、lint、类型、独立fixture类型与源码审查通过，替换浏览器CI仍待执行。
- H3 41689c0的[CI 38411607938](https://github.com/Kkwans/nas-control-plane/actions/runs/38411607938)Go/Compose通过，terminal包4.118s包含实际locale helper回归；完整CI因同一日志fixture失败而未通过（172通过/1失败），不是locale失败。

## 14:26 UTC Bash 能力探测资源边界

- H4三文件冻结并独立审查：保留Root、原参数/rc/脚本/环境，共享同一可比较writer对stdout+stderr合计最多32KiB；原750ms context新增100ms WaitDelay，同步Run保留唯一Wait责任。
- 仅完整成功且context有效、未超限时解析；任一失败三个能力布尔值全部false，errors.Join保留运行/取消/超限原因，不记录捕获内容。共享parser、容器路径和ready-FD未改。
- 精确16项fake/config/parser/主机及容器metadata选择器0.790s、race1.801s通过；独立最终race1.802s，全包仅编译、vet、本机构建和冻结hash/diff通过。实际helper仅编译，fd1/fd2继承管道、双流输出与取消行为待隔离CI。
- 容器探测的截断、非零部分成功、状态确认与非TTY多路帧解析仍需单独设计；H4不宣称覆盖所有探测生产者，也不承诺硬墙钟或任意后代回收。
- b5c4844已推送，最终日志fixture复验[CI 38412715113](https://github.com/Kkwans/nas-control-plane/actions/runs/38412715113)进行中，真实下载与模拟SSE的覆盖边界保持明确。

## 14:27 UTC 日志导出与locale完整CI闭环

- [b5c4844 CI 38412715113](https://github.com/Kkwans/nas-control-plane/actions/runs/38412715113)整体成功，51文件610项单元、173/173项Chromium，Go/Compose通过，发布跳过。
- 最后一项实时快照场景通过：模拟日志事件到达后页面更新，但真实下载文件仍是点击前快照且事件源未重启。37354be八图视觉闭环适用于未变化的前端产品代码；不将模拟事件源描述为真实网络SSE。后代完整CI同时覆盖H3实际locale helper。
