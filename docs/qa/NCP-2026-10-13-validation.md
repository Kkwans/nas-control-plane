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
