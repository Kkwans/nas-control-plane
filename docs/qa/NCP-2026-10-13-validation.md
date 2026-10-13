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
