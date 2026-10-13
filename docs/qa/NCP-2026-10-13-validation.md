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
