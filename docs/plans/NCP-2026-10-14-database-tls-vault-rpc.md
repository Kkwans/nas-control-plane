# 数据库 TLS T2：vault / RPC 防降级实施设计

日期：2026-10-14。设计审查基线：`3ea4264`；T1a连接器基础已提交`6f4a7e9`。本文件分开记录实施要求与文末实际检查点；设计不等于功能已启用。无真实凭据、NAS/PC、监听器或数据库连接操作。

## 1. 结论和必须调整的计划

1. **先合入并发保存守卫，再准备私有 v2 codec，最后以一个原子集成提交接入 Credentials、Manager、RPC、vault 和 HTTP resolver。** 不把 `Credentials.TLS` 或 JSON 字段单独提前合入。
2. 旧 v1 ciphertext/AAD/解密 payload 的行为保持不变。TLS 必须使用 v2，v2 的版本号绑定进 GCM AAD，且明文 payload 必须包含有效的非 nil verify-full 策略。
3. 新 namespace 推荐 `/ncp.agent.v1.AgentDatabaseTLSService/`，包含完整八个建连操作。不做 capability 探测后调用旧方法，不在 Unimplemented 后重放。
4. **若“ABA”包括删除重建、空记录新建再删除和状态恢复，单纯完整行 CAS 仍不够。** 推荐 credential migration 3 增加一个保留 tombstone 的 source revision 表和触发器；不增加 TLS 列，也不重新加密历史数据。这比计划里“无 SQLite schema 变化”的方案强，须在实施前同步修订计划说明原因。
5. 源码中 `/test-connection` 与手动 `ResolveConnection` 目前绕过 vault 策略，必须一同修复。仅保护 `ManualConnect` 的保存不够。
6. Resolver 的失败自动禁用也必须条件更新。现有“读旧记录失败，按 source ID 禁用”会误伤刚保存成功的新 v2 记录。

### 已核实的源码依据

- `internal/database/credentials_store.go:122-148`：仅 migration 1/2；metadata 没有 revision。
- `credentials_store.go:253-289`：`PutCredential` 是无条件 UPSERT，existing row 的 created_at 保留。
- `credentials_store.go:326-343,345-388`：禁用和 rotation 只按 source ID 更新；`RowsAffected` 的 error 目前被丢弃。
- `credentials_store.go:238-249,283-284`：时间精度只有 Unix 秒。同秒比较必然不够。
- `crypto.go:301-306,383-385,408-412`：seal/open 都显式复制四个字段；增加 Credentials 字段不会自动存取。
- `crypto.go:347-349,415-416`：旧实现只接受 envelope v1；AAD 是 `ncp/database-credentials/v1/<source>/<driver>/<endpoint>`。
- `connection.go:50-76`：手动 resolver 不读 vault；SQLite 提前返回。`ManualConnect` 是先测试、后无条件 Save。
- `internal/httpapi/database_connection.go:35-53`：`/test-connection` 直接调用 Agent，没有 resolver。
- `database_connection.go:104-139`：catalog/query/mutations 的 credential presence 只检查四个字符串。
- `internal/agentsocket/database_service.go`：八个操作通过普通 `json.Unmarshal` 解码，旧 Agent 会丢弃未知 TLS 字段。
- `internal/agentsocket/database_client.go:77-95`：QueryWithOptions 已有“不回退普通 Query”的先例，但 TLS+resultMode 要避免先把 Unimplemented 错映射成 result_mode_unsupported。
- `internal/agentsocket/server.go:202`：当前无条件只注册旧 Database service。
- `internal/database/manager.go:383-413`：所有八个入口最终汇入 `connect`，适合一次统一分派。
- T1a已实现的包内接口：`newDatabaseTLSConnector(source Source, credentials Credentials, policy *databaseTLSPolicy) (driver.Connector, error)`。它拒绝 nil，不拨号，仅返回 per-instance connector；错误为无 cause 的 `errDatabaseTLSConfigurationInvalid`。Manager 非 nil 分支用它加 `sql.OpenDB`；nil 分支保留原 `connectionString` / `sql.Open`。

## 2. 不得被拆坏的安全不变量

- Legacy nil 仍是既有行为，不能称为“安全 TLS”或经验证的明文。
- 严格请求的 policy 在测试前冻结。密码、TLS 指针、source binding 和 CAS token 同时冻结；测试后不再继承新策略。
- 测试失败不写入；保存冲突不重测、不重放密码、不自动把新策略拼到旧测试结果。
- 所有凭据替换入口都必须有测试前的 expected token；不存在“不带 expected 的兼容 Save”。
- v2 open 后的 TLS 缺失、null、空对象、空/未知模式、坏 PEM/CA/显式身份均为 credential_corrupt。
- 严格 RPC handler 自己检查 policy，不能只相信客户端；严格 provider 必须明确承诺支持 TLS。
- 严格 service 上 Query 和 QueryWithOptions 是两个不同的方法；明确 resultMode 不转普通 Query。
- automatic=false、已禁用、source 变化或 key 不可用都不等于允许丢掉已保存的 TLS。
- 当前代码的 CAS 无法约束旧二进制的无条件写入。部署时不得让旧/新 vault writer 并发共享同一文件。此次设计不执行部署调整。

## 3. 建议提交边界：每片可编译、可合入

### A. 并发存储守卫，完全没有 TLS 字段

改 `credentials_store.go` / `connection.go` / `errors.go` 及对应测试：

- migration 3 revision/tombstone 表及 triggers。
- 原子 snapshot/CAS 接口、CAS rotation、CAS disable。
- `ManualConnect` 在测试前读取 snapshot，测试后只按该 snapshot 保存。
- 去掉无条件生产 `PutCredential` / 无 expected 的 vault `Save`，测试 fixture 改为受控 helper。
- 新增 `credential_conflict` 安全错误码，并同步 HTTP/OpenAPI 409 合同。旧 nil 连接一般结果不变；真正发生竞争时现在拒绝覆盖。

这一片与 T1 独立，可先合入；没有 v2 writer、TLS JSON 或新 RPC service。

### B. 私有 v2 codec 与 policy 验证基础，不激活外部 TLS

新增私有载体 `credentialValue`，持有现有四字段 Credentials 和包内 `*databaseTLSPolicy`。现有 public Encrypt/Decrypt wrapper 继续仅处理 v1；新私有 versioned codec 在测试中覆盖 v1/v2。rotation 与普通加密共用同一个 envelope builder，避免复制两套版本选择代码。

可以提取 T0 的共用 intrinsic policy 验证函数，但不得改变 T0 冻结语义。此时没有 `Credentials.TLS`、HTTP TLS 输入或已注册 strict service；普通生产调用仍然读写 v1。v2 fixture 只在内存和临时 SQLite 中。

### C. 原子集成提交：所有 guard 齐备才出现 Credentials.TLS

同一个编译提交完成下列内容：

- 导出最终 `TLSPolicy` 类型并加 `Credentials.TLS`；接通 clone、versioned crypto 和 vault。
- Manager 非 nil→T1 connector，nil→原 DSN；严格错误映射。
- 新 service/client 的八方法路由、handler 强校验、provider 明确 opt-in。
- manual/auto/resolver/test-only 的统一策略准备；SQLite 不丢弃 policy。
- HTTP 所有入口的 resolver 守卫及 TLS-only presence 判断；稳定错误码映射。
- 保留一个非常小的 HTTP rollout gate：在 `decodeDatabaseBody` 的 bounded raw JSON 上检测 `credentials.tls` 键，只要键存在就拒绝为 DATABASE_INPUT_INVALID，包括 null。这使内部 RPC/vault 可以完整验证，但 HTTP 尚不开放新字段。
- OpenAPI 在此时只更新新增可见错误，不宣称 TLS 输入可用。T3 同步发布 TLS input schema 时删除 rollout gate；UI 可以同片或稍后开放。

如果不想临时 HTTP gate，也可以让 C 同步完成 T3 的 HTTP/OpenAPI 输入合同，明确把它作为首次 API 开放；前提仍是 C 包含所有守卫。**不能为了让提交更小，先提交 TLS JSON、后提交 Manager/RPC/vault。** 不推荐 `TLS json:"-"` 加各处自定义 RPC serialization：它会额外制造两套 DTO 和漏字段风险。

A/B 的独立性是真实的；C 是有意保留的最小安全集成原子边界，不能拆成不安全的可运行中间状态。

## 4. 精确的 store / revision 合同

### 4.1 为什么不用现有列，或只加一列

- 全量比较 ciphertext、key version、driver、endpoint、automatic_enabled、last_error_code 可挡普通并发替换，且随机 nonce 使正常重复保存的密文不同。
- 但它不能证明“期间没有修改”：相同密文删除后重插、automatic false→true、读到不存在后别人 insert→delete 都能回到原值；秒级时间也可以完全相同。
- 单个 row revision 列能挡存活行 ABA，但删除后 revision 重置，仍挡不住上述不存在或 delete/reinsert ABA。
- 进程锁不能覆盖第二个 SQLite adapter / 进程；持有事务跨远端测试会锁住共享控制面数据库，不可接受。
- 因而选**保留每个 source 最后 revision 的独立表**。一个 migration，没有 TLS 明文 metadata，也不碰密钥文件。

### 4.2 migration 3 的 DDL

在现有 `credentialMigrations` 追加 Version 3；所有 DDL、backfill 和 migration ledger 写入仍由 `applyMigration` 同一短事务完成。

```sql
CREATE TABLE ncp_database_credential_revisions (
    source_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL
        CHECK (typeof(revision) = 'integer' AND revision > 0)
);
INSERT INTO ncp_database_credential_revisions (source_id, revision)
    SELECT source_id, 1 FROM ncp_database_credentials;
```

增加四个 trigger：

1. AFTER INSERT：给 NEW.source_id UPSERT revision=1 / existing+1。
2. AFTER DELETE：给 OLD.source_id 同样 UPSERT；**不删除 revision row**。
3. AFTER UPDATE OF driver, endpoint, username, database_name, ciphertext, key_version, automatic_enabled, last_error_code，且至少一列 `OLD.x IS NOT NEW.x`：递增 revision。
4. BEFORE UPDATE OF source_id，当实际改变 ID 时 `RAISE(ABORT, 'credential source cannot change')`。现有代码从不原地改 source ID；重命名应是明确的删除/新建。

每个递增 trigger 的 body：

```sql
INSERT INTO ncp_database_credential_revisions(source_id, revision)
    VALUES(NEW.source_id, 1)
    ON CONFLICT(source_id) DO UPDATE
    SET revision = ncp_database_credential_revisions.revision + 1;
```

DELETE trigger 用 OLD。UPDATE 的完整 WHEN 是上面八个列的 `IS NOT` OR。

**不增 revision 的列：** created_at_unix、updated_at_unix。当前根本没有 last_used_at；以后加入单纯 read/bookkeeping 字段也不要加入触发器。所有 policy/secret 变化位于 ciphertext，rotation 也变化 ciphertext/key_version；automatic/last_error 反映能否自动使用，纳入比较。

**溢出：** 9223372036854775807 + 1 会失去 SQLite integer 类型并触发 CHECK，整个 credential statement 原子回滚；不 wrap/reset，也不回退无 revision 写入。数据库层失败映射 credential_store_unavailable（rotation 外层 key_rotation_failed）。如果希望更清晰的固定内部错误，可在 trigger 中先对 max 值 RAISE；不是必要条件。

**fresh source：** 不存在 revision row 表示 revision=0。曾出现后删除的 source revision>0 且 Exists=false。revision row 长期保留，不能正常清理，否则重启 ABA 保护。tombstone 只多存已有 source identifier 和一个整数，不存密码或 PEM。

### 4.3 类型和接口

建议精确调整为：

```go
type CredentialBaseline struct {
    SourceID string
    Revision int64
    Exists   bool
}

type CredentialSnapshot struct {
    Baseline CredentialBaseline
    Record   StoredCredential // Exists=false 时零值
}

// StoredCredential 新增 Revision int64，非 API 字段；来自 join，不是 credentials 表列。
// Rotation 中 Expected.SourceID 必须与 SourceID 一致。
type CredentialRotationUpdate struct {
    SourceID string
    Expected CredentialBaseline
    Ciphertext []byte
    KeyVersion int
    AutomaticEnabled bool
    LastErrorCode ErrorCode
}
type CredentialRotationDisable struct {
    SourceID string
    Expected CredentialBaseline
    Code ErrorCode
}

type CredentialStore interface {
    Migrate(context.Context) error
    GetCredential(context.Context, string) (StoredCredential, error)
    ReadCredentialSnapshot(context.Context, string) (CredentialSnapshot, error)
    CompareAndSwapCredential(context.Context, CredentialBaseline, StoredCredential) error
    ListCredentials(context.Context) ([]StoredCredential, error)
    DisableAutomaticIfUnchanged(context.Context, CredentialBaseline, ErrorCode) error
    ApplyRotation(context.Context, []CredentialRotationUpdate, []CredentialRotationDisable) error
    DeleteCredential(context.Context, string) error
}
```

Get/List 返回同一 statement 中 join 到的 Revision，不能先查 record 再另查 revision。ReadSnapshot 必须用单条 SELECT：以 `(SELECT ? AS source_id)` 为 anchor，LEFT JOIN revisions 和 credentials；对 credential 的 NULL 列使用 NullXXX/存在标志解码。这一条返回即使完全不存在的 source，也带 revision=0。复制 ciphertext slice，禁止 caller mutation 改 expected 数据。

Remove `PutCredential` from the production interface and implementation, rather than retaining an attractive unguarded escape hatch. Initial fixture inserts can use snapshot+CAS helper; raw SQL exists only in migration/explicit synthetic tests. DeleteCredential 仍可用于明确删除；触发器记录 tombstone。

### 4.4 精确 CAS

- Existing：普通 UPDATE（不 UPSERT），WHERE source_id=? AND EXISTS (SELECT 1 FROM revisions r WHERE r.source_id=credentials.source_id AND r.revision=?)；created_at 不变。
- Absent：INSERT … SELECT values WHERE NOT EXISTS(current source) AND COALESCE((SELECT revision FROM revisions WHERE source_id=?),0)=expectedRevision ON CONFLICT(source_id) DO NOTHING。
- baseline.SourceID 与 record.SourceID 必须完全相同；Expected.Exists=true 时 revision>0，false 时允许 0 或 tombstone revision。
- RowsAffected 必须正好 1；0→固定 `credential_conflict`；RowsAffected 自身出错→store unavailable；不能忽略 error。
- CAS 不需要先持锁读取：单条 SQL 的 WHERE、写入、trigger revision 更新在同一 statement 下原子完成。SQLite 第二个连接的写会被串行化，不能穿过 WHERE/write 边界。
- SQLite busy/locked 错误不当成 CAS 成功，也不重新测试。若以后加数据库 busy 重试，只能重试同一 prepared write/token，绝不能重新读取 baseline 后覆盖；当前 5s busy timeout 可保持。
- CAS 对新代码控制的所有替换生效。触发器也会记录旧程序对列的写，但**不会让旧程序的无条件 UPSERT 自己具有 CAS**。回滚后 v1 仍可读；v2 被旧程序拒绝并可能禁用自动连接；不产生降级副本。
- 两个迁移进程同时启动是现有 Migrate 的独立竞态：appliedMigrations 在事务外读取。若本片顺手加固，只在迁移短事务内重新确认 version 并处理另一个进程先提交；不把它变成长事务，也不移除 migrations ledger。至少验收重复 Migrate 幂等，不声称当前已有并发 migration 保证。

### 4.5 Rotation 与 disable

`ListCredentials` 的每条记录携带 revision。构建 rotation update 时带原 baseline，在一个短事务里逐条 CAS。任一 update 冲突，全部 re-encryption 更新回滚，保留所有当前 ciphertext；返回 key_rotation_failed，其安全 cause 可为 credential_conflict。

- **CAS conflict 单独处理**：不要转入现有 `disableAllAfterRotationFailure` 重新 list 并禁用刚刚修复成功的新记录；冲突直接失败，不动并发成功的凭据。
- 真正 key rotation / decrypt / encrypt 失败保持 fail-closed：对明确抓取的受影响 snapshot 执行 conditional disable，ciphertext 不变。若某个 snapshot 已变，则跳过该记录，不追着新 revision 重试禁用。尽力禁用失败应被记录为安全失败，不假称全部禁用完成。
- `Resolve` 的 source mismatch/decrypt 失败也只 `DisableAutomaticIfUnchanged(snapshot.Baseline, code)`。失败处理不覆写新凭据，不把 secondary conflict 替换为调用者真正的解密错误。
- explicit administrative DisableAutomatic 若仍需要对“当前记录”操作，可保持外层 vault 方法，它先读取 snapshot 后做条件禁用；不要暴露无条件生产 store update。

### 4.6 Key rotation 的跨资源提交边界

现有 `Rotate` 先调用 KeyProvider.Rotate，再 list/decrypt/encrypt，最后 SQLite ApplyRotation。`FileKeyProvider.Rotate`（crypto.go:183-209）先将新 key 加到保留历史 key 的 ring 并原子 rename（246-277）；它和 SQLite transaction **不是一个跨资源原子提交**。

- SQLite CAS 冲突/回滚时，不回滚 key ring currentVersion，不删除新 key。并发 save 可能已经使用新 key，回滚 key file 反而会使新 ciphertext 不可解密。
- crash 在 key file 提交后、SQLite 提交前：旧 ciphertext 仍靠保留的旧 key 解密；新 key 可以暂时没有记录使用。SQLite 全部 rotation updates 必须全提交或全回滚。
- crash 在 SQLite 提交后：需要 new key 已先安全保存。当前代码 Sync 临时文件再 rename，但没有父目录 fsync，不能把这称为跨断电的完整原子保证；T2 不扩展为密钥存储重构。保持先发布 key、后写 ciphertext 的次序。
- 可把 `Rotate` 返回的 `KeyMaterial` 固定传给 `reencryptWithKey`，避免 currentVersion 在两次 Current 查询间漂移；无论采用返回值还是现有 Current，都必须保留历史 keys。混合 keyVersion 的行在既有 ForVersion 合同下有效，不得把“全部同版本”当读取前提。
- 对并发 rotation，CAS 使冲突的一方不能覆盖较新的 records；可加 vault 内 rotation mutex 避免同一实例重复工作，但它不替代 SQLite CAS，也不能宣称是跨进程 key-file 锁。FileKeyProvider 的 mutex 仅保护单实例；继续要求单一受控 key-ring writer，不新增协调架构。
- 当前 FileKeyProvider `nextVersion<=0` 时回到1、MemoryKeyProvider 直接 current++，都可能在整数溢出时复用 keyVersion。若此次触及它们的 rotation 逻辑，应改成固定 key_rotation_failed，绝不复用已有版本；无需为此引入新 schema。
- fixture 要证明：CAS失败后 currentVersion已增加、旧/并发新密文仍分别可解密，且未产生 v2→v1；模拟 key provider 丢失旧 key 的已有失败测试仍应保留 ciphertext并有条件禁用。

## 5. vault / coordinator：冻结策略再测试

### 5.1 私有 prepared 对象，去掉无 expected 的 Save

```go
type preparedCredentialWrite struct {
    source Source
    credentials Credentials
    baseline CredentialBaseline
}
func (v *CredentialVault) prepareManual(ctx context.Context, source Source, manual Credentials) (preparedCredentialWrite, error)
func (v *CredentialVault) savePrepared(ctx context.Context, prepared preparedCredentialWrite) (SavedCredential, error)
func cloneCredentials(Credentials) Credentials // TLS 指针独立复制
```

`savePrepared` 只被 `ConnectionCoordinator.ManualConnect` 成功测试分支调用。移除旧 public `Save(ctx,source,credentials)`；仓库目前除 ManualConnect 外只有 database_test.go 的一个 setup 使用它，改为 fake-success coordinator 或包内 fixture helper，无必要保留危险兼容入口。

### 5.2 prepareManual 的精确次序

1. 检查 context / source；复制传入 credentials，包括嵌套 policy。读取唯一 snapshot（包含不存在的 source）。
2. **有明确 TLS**：验证新的策略和当前 source target，之后按该 snapshot CAS；可用于明确修复旧坏记录，不依赖解密旧密码。
3. **没有 TLS，已有记录**：不能只看外层 version 判断安全；解密并认证现有记录。否则将 v2 外层 version 篡成 1 后可能被当成 legacy 覆盖。
   - 已认证 v1 → 允许维持 legacy，无需把旧密码合并入新手动请求。
   - 已认证 v2 → 推荐在测试前复制旧严格 policy；automatic=false 也不能跳过此规则。也可固定拒绝要求显式策略，但不能变成 legacy。
   - 解密/key/policy 错误 → 在任何 Agent 调用前返回，不能未知即假设 legacy。可由用户明确提供严格 policy 修复，或通过单独授权的删除/重连流程处理；这是防降级所需的失败边界，旧损坏记录的任意明文修复不再安全。
   - v2 的 driver/endpoint 与当前 source 不一致 → 不自动继承，要求显式有效策略。比较旧 record.Database 形成的 endpoint；用户只改 database 名但物理 host/port 未变可保留原策略。
4. 针对 frozen source/credentials 验证目标并编译/验证 policy；只有完成后才调用 tester。
5. **TLS-only 输入不自动填用户名/密码。** 它算手动请求，仍要求本次 username 与有效 database；不把“只改 TLS”偷偷转换成保存凭据连接。密码为空仍遵循既有 password/token 行为，不另发明新的必填密码限制。
6. 返回 private prepared object。测试使用它的独立 credentials copy；成功后 savePrepared 使用内部 frozen original，而不是可能被 fake/provider 改动的 pointer。

ManualConnect = prepare → c.test(frozen) → savePrepared(CAS)。保存冲突返回 `credential_conflict` / operation manual_persist；不能说此凭据已经保存，不能自动重新测试。测试成功与持久化失败的已有保守 UI 语义要保留。

### 5.3 各入口的一致性

- `ResolveConnection(manual!=nil)` 调 prepareManual，同一策略规则；只返回 frozen Connection，不保存。
- `ResolveConnection(nil)` / AutoConnect 走 authenticated Resolve；v2 必须带有效 TLS 回到调用者，深复制。无记录报 credentials_required，不回退输入或明文文件。
- 新增 `ConnectionCoordinator.TestConnection(ctx, source, manual Credentials)`（或 `TestManualConnection`，统一选一个）用于 test-only：prepareManual→c.test，不 Save。
- HTTP `/test-connection` 先 discover source，然后调用这个受保护 test-only 接口；禁止调用 Connect，因为测试接口不应写 vault。若 configured coordinator 缺少 test-only extension，则 fail closed，不绕过已配置 vault。
- 如果没有 configured vault/coordinator，测试可保持现有 direct Agent 路径，因为没有可继承的保存记录；显式 TLS 仍强制 strict RPC。不要在有 coordinator 却缺方法时假装“未配置”。
- catalog/query/rows/insert/update/delete 的 prepareDatabaseConnection 全部复用 resolver；`hasDatabaseCredentials` 加 `credentials.TLS != nil`。
- SQLite：先拒绝 incoming 非 nil TLS，然后才走旧 local-path fast path；如果相同 sourceID 留有 v2 网络记录/target mismatch，也必须失败，不能提前丢掉 strict policy。正常没有 v2 的 nil SQLite 仍不要求 username/password，且不引入 TLS。
- Frozen baseline 是单次操作的 snapshot 一致性边界；CAS 保证旧测试不能覆盖随后保存的 strict record。它不是“把已发出的 legacy 网络测试撤回”的全局线性化保证；不得持 SQLite 事务覆盖网络时间来假装解决这一点。

## 6. v1/v2 crypto 精确变化

### 6.1 最终类型

C 片才导出类型；T0 包内类型可作为 alias 保留，避免 T1 大量改名：

```go
type TLSMode string
const TLSVerifyFull TLSMode = "verify-full"
type TLSPolicy struct {
    Mode TLSMode `json:"mode"`
    CAPEM string `json:"caPEM,omitempty"`
    ServerName string `json:"serverName,omitempty"`
}
// 现有 Credentials 增加 TLS *TLSPolicy `json:"tls,omitempty"`
```

`cloneCredentials` 复制 policy struct；其中 string 不共享可变内存。saved metadata 不增加 CA/密码/完整 TLS 对象，revision 也不进入公开 JSON。

保留 `credentialPayloadV1` 四字段及其 plain json.Unmarshal 行为。新 `credentialPayloadV2` 是四字段加 `TLS *TLSPolicy json:"tls"`，这里不使用 omitempty。private codec 载体避免 B 片提前修改公共 Credentials。

### 6.2 AAD 的确切定义

保留现有 `credentialAAD(sourceID,driver,endpoint)` 产生的 baseAAD **原样不动**。新增：

```text
aad(version=1, baseAAD) = bytes(baseAAD)
aad(version=2, baseAAD) = bytes("ncp/database-credentials/envelope\x00")
                         || uint32_big_endian(2)
                         || uint64_big_endian(len(bytes(baseAAD)))
                         || bytes(baseAAD)
```

这个 v2 域有固定 domain/version/长度，仍绑定原 source/driver/endpoint。不要对 baseAAD 字符串简单替换其中的“v1”，也不要尝试 v2 解密失败后用 v1 AAD 重试。

- Encrypt 的 envelope version 由已冻结 policy 选择：nil→1，valid nonnil→2；非 nil invalid 不加密。
- Decrypt 先检查允许的 envelope version、固定 algorithm、有效 keyVersion，再只选择对应的一种 AAD 和一种 payload codec。
- v1→v2 或 v2→v1 外层篡改必须在 GCM authentication 时失败，即使对应 key 相同。
- v1 sealed bytes 不重新格式化、不批量迁移；轮换仍按 v1 AAD 写回 v1。
- v2 解密以后要求 policy !=nil 并 intrinsic 验证；坏策略→credential_corrupt，返回零 Credentials。v2 payload 用 DisallowUnknownFields + EOF 检查，防止未来字段静默降级。v1 reader 则保持原未知字段容忍行为。

### 6.3 共用加密实现与验证分层

`EncryptCredentials` 和 rotation 的 `encryptWithKey` 必须汇入同一个 version/payload/AAD/envelope builder，不能在两条路径分别填 Version 常量。当前重复的 `sealCredential` / `encryptWithKey` 代码需要收敛。

建议 private helpers：

```go
func encryptCredentialValueWithKey(ctx context.Context, material KeyMaterial, baseAAD string, value credentialValue) ([]byte, int, error)
func decryptCredentialValue(ctx context.Context, provider KeyProvider, baseAAD string, encoded []byte) (credentialValue, error)
func credentialEnvelopeAAD(baseAAD string, version int) ([]byte, error)
```

纯 crypto API 只有 opaque baseAAD，不能独立得知真实拨号 host。它验证 mode/CA/显式 serverName 的 intrinsic 部分；vault 对 record 的 driver/endpoint 和 Manager 对实际 source 分别做完整 T0 target 编译。不要解析 AAD 的斜杠去推导目标。

提取 `validateRequiredDatabaseTLSPolicy`，让 mode、CA parsing、显式名字规则复用 T0 helper；不要以 compiler(nil) 的成功作为严格验证，也不要复制第二套较宽松 PEM/name 规则。对跨包 RPC 可导出 `ValidateRequiredTLSPolicy(*TLSPolicy) error`，返回固定 code。它不接受 nil，不拨号、不读系统根。

## 7. RPC：八方法严格 namespace 与 legacy 隔离

### 7.1 方法清单

新增 service name `ncp.agent.v1.AgentDatabaseTLSService`；方法仍分别强类型，request/response 形状不改成通用 exec：

- TestConnection
- Catalog
- Query
- QueryWithOptions
- Rows
- Insert
- Update
- Delete

Discover 不建连，继续旧 namespace，不新增 strict Discover。

### 7.2 Provider 与 registration

保留旧 DatabaseProvider、ConnectionProvider、QueryOptionsProvider 接口不变。新增显式 composite：

```go
type DatabaseTLSPolicyProvider interface {
    DatabaseProvider
    DatabaseConnectionProvider
    DatabaseQueryOptionsProvider
    SupportsDatabaseTLSPolicy() bool
}
```

Manager 只有在非 nil dispatch 已接上 T1 后才实现 true。`newDatabaseTLSService` 只接受这个 composite，并拒绝 false；server.go 只在断言成功且 true 时注册严格 service。旧或任意 custom provider 仍只注册旧 service。

严格 `AgentDatabaseTLSServiceServer` 的八方法都必需；不要复用旧接口的可选 QueryWithOptions/TestConnection 分支来默默提供不完整 strict service。可让 wrapper 实现包内 marker，保证专用 registration 不能不小心接收普通 `newDatabaseService(oldProvider)`。

这是一条受信实现的明确 opt-in 合同，并非远端 capability 探测。不能把 marker 加在所有 provider 的通用 adapter 上。

### 7.3 每个 handler 独立验证

最少重复实现：`databaseService` 增加 `requireTLS bool`，每个 handler 解码后、任何 provider 调用前执行统一 guard。

- strict：credentials/tls 必须非 nil，完整 intrinsic verify-full 验证；缺失/null/{}/空mode/unknown mode/bad CA/bad显式name 全拒绝。
- legacy：一旦 Credentials.TLS 非 nil，返回 tls_policy_unsupported；不要把已知严格请求经旧 namespace 转发给可能忽略字段的 legacy provider。
- strict decoding 可用新 `decodeStrictDatabaseRequest`（DisallowUnknownFields + EOF），不改变 legacy decoder 的兼容语义。
- malformed policy 固定 tls_configuration_invalid，不带字段值、PEM 或解析 cause；整体坏 JSON/类型可用固定 sql_invalid 或固定 gRPC InvalidArgument。
- handler 不掌握 discovery 的真实 driver/host；此处检查 policy 自身。Manager 在任何 opener 前用实际 source 完整验证 driver/host，拒绝 SQLite TLS。不能信任客户端额外传一个声称的 host 来代替 source lookup。
- strict QueryWithOptions 同时调用 ValidateQueryResultMode，并使用 provider.QueryWithOptions；未知 resultMode 不到 provider。

### 7.4 Client routing 与错误优先级

保持 `callDatabaseClient` 的数值文本化、protobuf budget、错误去敏逻辑。为 dialing wrapper 统一增加 `callDatabaseConnection(... connection Connection, request, result any, ...)`，由 typed wrapper 显式传入 Connection；不要对 any request 靠遗漏易发的反射猜是否 TLS。

- request.Credentials.TLS=nil → existing client / existing paths，不改变行为。
- nonnil → 本地 ValidateRequiredTLSPolicy，构造 strict client / strict method paths，只调用一次。
- Test、Catalog、Query、Rows、三个 mutations 都通过此 helper；mutateDatabase 要显式接收 connection。
- Discover 继续 callDatabase，不查看 policy。
- 可在 low-level client 增加 service prefix，或新增八方法 TLS client。避免把 strict Discover 写成 legacy fallback；strict service 没有 Discover。
- TLS nonnil + ResultMode="" → TLS/Query。
- TLS nonnil + ResultMode auto/rows/exec → TLS/QueryWithOptions。
- TLS nil + 显式 ResultMode → 现有 QueryWithOptions，原 result_mode_unsupported 语义不变。

**错误顺序重要：** 现有 queryDatabaseRPC 会立刻把 Unimplemented 变成 result_mode_unsupported。严格路径要先把任何 strict namespace 的 Unimplemented 转为 tls_policy_unsupported，再进入公共 error sanitization；不要让旧 helper 先吃掉它。可以为 strict query 单独调用八方法 client，legacy helper 保持不动。strict namespace handler 明确返回的 result_mode_unsupported 则可原样保留。

不要旧路径 retry、去字段 retry、普通 Query retry；不要为 writes/mutations 做透明 failover。

## 8. 错误、HTTP 和兼容合同

新增并加入 CodeFromString、RPC、HTTP、OpenAPI、前端 known-code 列表：

- `tls_configuration_invalid`：400；本地/handler/target policy 无效；固定安全文字。
- `tls_policy_unsupported`：400；旧 Agent / 未注册 strict provider；提示升级 Agent，不自动重试。
- `tls_verification_failed`：400；后续真实 TLS/x509 错误按类型分类，不解析 raw 字符串、不留原始证书/driver error chain。
- `credential_conflict`：409；test 后保存 baseline 已变；说明本次凭据未保存，需要用户重新确认后手动重试。

v2 已保存 payload 验证失败对外仍是 `credential_corrupt`，不能把坏存储当成新用户配置错误；rotation 外层仍 key_rotation_failed。

不要因新增 TLS code 就把 SQL/write outcome 映射为“肯定未执行”。严格入口失败确实在 provider 前，但既有前端对超时/未知/响应损坏的保守处理要保留；T3 必须继续保持连接已成功、目录失败与保存结果未知的区别。

旧 Server 的 HTTP DisallowUnknownFields 会拒绝新增 TLS；旧 Agent 根本不认识新 namespace。回滚旧 Server 看到 v2 会拒绝、可能自动禁用；恢复升级后可能需用户手动重连。不能承诺旧 binary 任意手动输入都被新 envelope 保护。

## 9. 无监听器验收矩阵

### 9.1 Store / CAS

使用 t.TempDir 下新 SQLite、NewMemoryKeyProvider 和 fake tester；所有密码/密文都是测试生成值。两条独立 SQLite connection 指向同一个临时文件，用 channel/latch 确定 interleaving，不使用 sleep 猜竞态。

1. 同秒 A snapshot→B save v2→A save v1 必须 conflict，最终 B ciphertext/automatic 保留。
2. initially absent A→B strict insert→A save 必须 conflict。
3. initially absent A→B insert→B delete→A insert 必须 conflict（empty ABA）。
4. A existing snapshot→删除→重插完全相同 ciphertext/时间/metadata→A write 必须 conflict。
5. automatic true→false→true、last_error 空→非空→空、driver/endpoint A→B→A 均 conflict。
6. 只有 updated_at/created_at 变化不 conflict；无 policy/secret/state 变化的同值 UPDATE 不 bump。
7. rotation v1 snapshot→并发 v2 save→ApplyRotation 必须整体回滚，v2 保留且不会被错误路径禁用。
8. 两条 rotation updates，第二条冲突，第一条已执行 UPDATE 必须由事务回滚。
9. stale Resolve 解密失败→并发新 save→conditional disable 不伤新记录。
10. max int revision 上的写完整回滚，ciphertext 不变；fresh baseline0 与 tombstone baseline>0 区分。
11. migration 3 从 v1/v2-schema fixture backfill，重复 Migrate 不重设 revision；旧形状 UPSERT/UPDATE/DELETE 会触发 bump。
12. existing owned/external/shared SQLite 行为不回归，不擅自修改 journal_mode、pool、foreign_keys 或运行文件权限。

### 9.2 Crypto

- 已知 synthetic v1 golden envelope 在新 reader 成功（四字段、原 AAD）；nil write/rotation 仍 version1。
- v2 round-trip 和 rotation 均保留 policy，deepcopy 无别名。
- 同一 nonce/cipher/key，仅改外层 Version 1↔2 均 authentication failure。
- 必须构造**有效认证**的 malformed v2 payload：用 package test helper 直接 GCM seal 缺tls/null/{}/空mode/未知mode/坏PEM/坏name；不能只篡 ciphertext 再声称测到 payload validation。
- wrong source/driver/endpoint、wrong key、wrong nonce、unsupported version 都固定安全失败。
- old-v1-decoder fixture 按原 `version!=1` 拒绝 v2，禁止用当前新 reader 模拟旧行为。
- 普通 Encrypt 与 rotation envelope builder 的 version/AAD 选择必须共享并有测试。

### 9.3 RPC 八操作

实现 recording `grpc.ClientConnInterface` fake：Invoke 记录完整 method 和 payload；NewStream 固定未实现。无 grpc.NewClient、Dial、Serve、bufconn.Listen。旧 Agent fake 对新 namespace 返回 Unimplemented；每项只一次 Invoke、无旧路径调用、mutation provider 次数0。

每个八操作都跑以下 matrix：legacy nil；valid strict；credentials missing；tls missing；tls null；tls {}；mode空/未知；坏CA；坏serverName。后七类严格 handler 的 provider calls 必须为0。valid 路径用显式 opt-in fake provider；false或legacy provider 不可注册严格 service。

在内存直接调用 ServiceDesc handler（自己提供 protobuf decoder），并覆盖有/无 unary interceptor 的 FullMethod 是新路径。单纯调用 wrapper 方法不够验证实际 registration 路径。

用 structpb.NewStruct→proto.Marshal→proto.Unmarshal→handler/client codec round-trip，确认嵌套 policy 完整，数字 values/keys 仍按既有文本传递，response budget 未削弱。

Query 做四组合：TLS nil/non-nil × resultMode omitted/explicit；再覆盖 auto/rows/exec。严格组合的 Unimplemented 为 tls_policy_unsupported，legacy explicit 为 result_mode_unsupported，都无 replay。

### 9.4 Coordinator / HTTP

- test success才save；失败不覆盖；CAS后不继承新policy、不自动重测。
- 已有 v2 + omitted policy → 测试前继承（或按选定合同拒绝），包括 disabled record；继承的 TLS 与旧值深复制。
- source变更、坏v2、不可用key、伪造version1 均不能走 legacy tester。
- test-only 经过 guard但记录/Revision完全不变（失败引发的明确conditional disable除外，测试本身不保存新凭据）。
- manual resolver/catalog/query/mutations 都保留 policy；TLS-only按手动validation失败，不触发自动取旧密码。
- SQLite显式policy在tester/opener之前拒绝；nil SQLite既有路径保留。
- `httptest.NewRequest`+`NewRecorder` 直接调用 handler，不用 httptest.NewServer。
- C片的HTTP gate拒绝tls键包括null；T3删除gate后同步OpenAPI并冻结unknown TLS fields/empty mode语义。

### 9.5 Manager / T1 集成

用 fake opener/connector，不调用真实 driver Connect。证明非nil仅进 newDatabaseTLSConnector→sql.OpenDB 路径、nil仅原路径；strict配置错误时无legacy opener调用；Ping失败关闭pool；context/pool上限/成功关闭不回归。TLS类型错误映射用合成 x509/tls error fixture，不做握手。

注意现有 `newQueryTestClient` 在 `database_query_test.go:60` 使用 bufconn.Listen、grpc.Serve/NewClient。虽然是内存传输，它仍是 listener；本次无监听器限制下不要复用这个 helper，不要为新的RPC验收直接跑包含这些fixture的整包测试。

## 10. 已完成的最小设计验证与剩余边界

- 设计阶段已运行 SQL 原型：Python 标准库 SQLite `:memory:`，合成记录。验证了所提触发器 SQL、same-second状态ABA、delete/reinsert ABA、absent ABA、timestamp-only no-bump、CAS条件和溢出全statement回滚，全部通过。
- 这个结果仅验证 SQL 方案，不等于 Go modernc adapter 集成、GCM/RPC/HTTP 或真实 TLS 验收；后续实现状态见下方检查点。
- 设计阶段已核对 source inventory；不把设计审查记为功能实现。
- migration 3 是相对于已提交方案的明确设计修订。若坚持零schema变化，只能把保证收窄到“受控新写入不会复用密文/完整metadata，且不存在delete/reinsert/empty ABA”；这不满足此次明确的完整ABA要求，因此不推荐。
- 不更改 PG*、证书目录、真实凭据、NCP服务、NAS或开发者电脑。功能源码、测试与CI结果都不能描述为已部署。


## 11. T2-A0 存储基础检查点（2026-10-14 07:10 UTC）

首片A再分为A0基础与A1激活：A0只增加migration3保留版本/删除墓碑、触发器、同语句快照，以及SQLite条件保存/禁用/原子轮换方法。Get/List带回同语句revision。原CredentialStore接口和现有vault写入路径不切换，不能宣称生产并发保护已经生效；A1必须去掉无条件绕行并完成HTTP409合同。

18组新Go测试通过（重复20次），完整database与race、全仓构建/vet通过；独立复核包含REPLACE、两种recursive_triggers状态、删除重开、事务内ABA和溢出回滚。旧v1记录、重复迁移和迁移失败回滚均覆盖。使用真实临时SQLite和合成数据，没有读取或轮换真实密钥/凭据。

存储原语具备条件能力不等于TLS已启用；v2密文、严格RPC、Manager分派和HTTP/UI入口仍未激活。下一片优先把测试前快照接入ManualConnect，并将Resolve失败禁用、rotation全部切至条件写入，冲突不重测、不回滚已发布key。
