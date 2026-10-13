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
