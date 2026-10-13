# 站点图标条件写入归属

## 问题与边界

上传与删除图标先读取完整资料，再调用整行 upsert。手工站点在两步间被删除时，迟到请求会重建记录；并发资料编辑或忽略会被旧快照覆盖。两个上传持有相同旧文件名时，后完成者还会覆盖新归属。反向的资料 PUT 也可能把已删除的旧图标文件名写回。

本片只收敛图标归属，不改安全响应头、代理策略、客户端补偿次数或真实 NAS。禁止通过重读后再无条件写入来假装原子；不增加监听器、真实服务操作、自动重试或部署。

## 实施契约

- Store 新增 `CompareAndSwapSiteIcon(ctx, siteID, expectedName, replacementName) (bool, error)`。单条 UPDATE 按 project_id 与旧 local_icon_name 匹配，只修改 local_icon_name 和原有排序使用的 updated_at_unix；绝不 INSERT。仅 err=nil 时的 false 表示行不存在或预期归属已变化，不猜具体原因；err 非空时不能将 bool 当作未提交证明。
- 上传先写唯一新文件，再 CAS。成功后仅清理其获准替换的旧文件；冲突时仅清理本次新文件，不删除旧快照或其他请求的文件。CAS 返回错误时保留本次候选文件和旧文件，不补读数据库后猜测回滚；删除图标也必须 CAS 成功后才清理旧文件。HTTP 冲突使用稳定 409/SITE_ICON_CONFLICT，初始缺资料保持 404。
- 普通资料 Upsert 丢弃输入 localIconName；新行初始为空，已有行的图标保持数据库值。RETURNING 返回当前数据库图标，避免响应复述旧输入。保留其他资料校验、首次编辑发现站点的 INSERT 以及既有访问/忽略占位资料路径。
- 图标 CAS 独立校验站点 ID 和安全单文件名；空旧值/空新值有效，不要求资料有显示名，以兼容访问/忽略形成的空名占位行。
- 同步 HTTP 接口、内存 fake、OpenAPI 冲突语义及聚焦回归。更新客户端文档或历史结论时，不把本片描述为跨 HTTP 事务。

### 数据库错误不等于回滚

独立源码审查确认 modernc SQLite v1.54.0 的 stmt.exec 在 SQLITE_DONE 后仍可能因 context 取消返回 ctx.Err，因此数据库错误不能证明 UPDATE 没有提交。上传 CAS 的错误分支保留候选文件与旧文件，宁可留下待清理的孤立文件，也不能删除可能已被数据库引用的新文件。只有明确 false 且无错误时才清理本次候选；成功时才清理已替换的旧文件。本片不通过额外 SELECT 规避这一不确定性，不声称零孤立文件。

## 验证

2026-10-13：修改前从真实源码提取 UPSERT SQL，内存 SQLite 六组交错证明删除复活、编辑/忽略丢失、CAS 不插入、CAS 保留资料、竞争者单胜及反向普通 upsert 恢复旧文件名。此验证不是 HTTP 或 Go 驱动实测。

Store 实现后，以 Go 1.26.4、GOPROXY=off 执行新六组 `TestSiteIconOwnership*`（含子用例）及三组既有资料/访问兼容测试，通过；新六组测试的 race 检查也通过。测试只打开临时 SQLite，覆盖迟到删除、并发资料与访问、上传/移除竞争、资料写入不能指定/恢复本地文件、发现与占位资料、非法输入与取消 context。HTTP 实施者已报告限定的归属/冲突测试、空名自动资料测试、既有上传/本地/代理/回退安全与 multipart 测试以及 OpenAPI SiteProfile/SiteIconOwnership 契约测试通过。随后新增提交后返回错误的内存 fake 回归及保留候选文件行为，最终验证由主任务汇总，不将前次通过当作该后续改动已复验。完整构建与发布状态由主任务汇总。

## 最终精确快照复验

2026-10-13 22:31 UTC：以 f3c3517 为基线，仅复制本模块 11 个文件的精确快照。Store 与 HTTP 的 `TestSiteIconOwnership*` 在 race 模式连续三轮通过，包含提交后返回 context.Canceled 的候选文件保留回归；完整 `go test ./api/openapi` 通过。归档快照没有 Git 元数据，首次构建在 VCS stamping 阶段失败；随后使用 `go build -buildvcs=false ./cmd/...` 和 `go vet -buildvcs=false ./internal/controlstore ./internal/httpapi ./api/openapi` 均通过。这只禁用构建版本元数据收集，没有跳过测试或源码检查。所有本地验证限于临时 SQLite、文件与 HTTP recorder/fake，远端 CI 和真实 NAS 验证另行记录。

## 保留限制

- 文件名 CAS 不是永久代次或 tombstone；删除后其他路径重建同 ID、或空图标经历变化又回空的 ABA，不在本片提供永久拒绝保证。
- 普通 PUT、访问记录、恢复/忽略仍有自己的 INSERT 语义；不宣称整个站点生命周期禁止复活，也不修改既有整站删除未清理图标的行为。
- 不保证进程崩溃、文件清理失败或数据库错误下的端到端文件/数据库事务一致性；数据库错误可能已经提交，保留文件也可能形成孤立文件；没有后台垃圾回收。
- 历史数据库可能保存 URL 转义 project_id，而列表返回解码 ID。CAS 不为这种不一致自动补建规范化行；可能返回冲突，需后续明确数据迁移处理。现有读兼容不等于写入迁移已完成。
