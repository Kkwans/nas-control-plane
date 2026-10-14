# 数据库 TLS：现状核验与分阶段实现提案

日期：2026-10-14。只读审查基线：`b66ab09`，分支 `dot/ncp-modernization`。设计审查不操作运行参数、数据库凭据或 NAS，未启动监听器、连接数据库或安装依赖。文末另记实现进度，设计要求不等于已交付。

## 结论与首片建议

下一模块适合做“数据库连接的显式证书校验 TLS”，但不能仅在 JSON 增加 `tls` 字段、拼接 `sslmode=require` 后立即上 UI。最小首片是**独立的 TLS 策略校验与配置编译器**：纯输入产生新建的 `tls.Config`，用纯测试冻结证书信任、服务器身份与兼容语义；暂不连接现有 HTTP/RPC/凭据存储。它是下述 T1/T2 的直接共用基础，单独合入时必须标为“基础代码完成，用户 TLS 功能尚未提供”。

首次对用户开放的新增策略只有 `verify-full`，支持系统根证书或用户提供的 PEM CA，以及可选 `serverName`。不添加 `skip-verify`、`prefer`、`require`、`verify-ca` 或新的明文模式按钮；不改变旧连接默认。旧请求/旧保存记录的 TLS 缺省仍走原路径，不能把这种兼容行为宣传为安全 TLS。

须先解决两个静默丢失风险：

1. 旧 Agent 的 Struct→JSON 解码忽略未知字段，新增 TLS 字段会被丢弃，随后仍按旧方式拨号。显式 TLS 必须调用旧 Agent 不认识的专用 RPC 路径，收到 Unimplemented 时失败，不能重试旧路径。
2. 旧 Server 解密凭据后也会忽略未知 payload 字段。TLS 凭据必须使用旧 Server 会拒绝的新密文 envelope 版本，不能只给 v1 payload 加字段。新版本也要绑定进 AAD，防止只改外层版本号就去掉策略。

## 一、已核实的当前行为

### 1. 计划位置与执行边界

- `docs/plans/NCP-2026-10-13-backend-audit.md:100,126` 已指出 PostgreSQL `sslmode=disable`、MySQL 未配置 TLS，建议显式策略/CA/服务器名；没有现成 TLS 细分实现方案。
- roadmap M14 聚焦精度、SQL 结果与预算，database-precision 文档没有 TLS 实施片。应加一个数据库 TLS 专项方案及 roadmap 条目，不混入 RETURNING/结果传输后续工作。
- `AGENTS.md` 要求 Root Agent 独占连接与主机访问；HTTP/OpenAPI 同步；不返回密码/DSN；源码变更不等于部署。沿用这些边界，不重做 Agent 传输或全站 HTTPS。

### 2. Agent 实际建连路径

- `internal/database/manager.go:383–407`：每次操作发现 source，调用 `connectionString`，`sql.Open`，配置池上限 2/空闲 1，`PingContext`；调用方随后关闭池。测试连接、目录、SQL、行读取及行变更都汇入这个边界。
- `manager.go:435–442`：MySQL literal `mysql.Config` 设置用户名/密码、TCP host:port、database、ParseTime、utf8mb4；没有 TLS。随后 `FormatDSN`→`sql.Open`。
- `manager.go:458–464`：PostgreSQL URL 明确包含 `sslmode=disable`；但“DSN 要求禁用”不等于已经实测所有运行环境都明文。已安装 pgx 会继承 PG*、service/passfile/本地证书默认值，且 `sslrootcert=system` 会在内部把模式改成 verify-full，见下文。此审查没有读取运行凭据/环境或实际握手。
- SQLite 按路径打开，不涉及 TLS；不可把网络策略套给 SQLite。
- `internal/database/discovery.go:158–182,199–245`：Docker wildcard 端口映射通常变为 Agent 侧 loopback；URL 发现只提取 driver/host/port/database，丢弃 URL query/TLS 配置和用户凭据。不能从发现来源是 DATABASE_URL、localhost 或项目容器推断其证书身份或传输安全。

### 3. 凭据和页面

- `models.go:37–47`、`crypto.go:301–305` 只有 username/password/token/database。
- `crypto.go:383–385,408–412` 显式复制四字段，增加 `Credentials` 字段并不会自动进入密文或解密结果。
- `credentials_store.go:444–492`：记录保存 source/driver/endpoint/database 等元数据与 AES-GCM 密文；当前 AAD 为 `ncp/database-credentials/v1/<source>/<driver>/<endpoint>`。source endpoint/driver 变化会关闭自动凭据；应保留这个绑定。
- `connection.go:92–105`：手动测试成功才保存；测试失败不覆盖已有记录；后续 catalog/query 由 resolver 解析保存凭据。这个顺序不变。
- `frontend/src/views/DatabaseDetailView.vue:380–410` 只有用户/密码/库名和测试/连接按钮，没有 TLS 选择或安全状态。
- `frontend/src/stores/database.ts:99–117` 对凭据做浅复制，以 string 非空判断手动输入；成功连接后读取目录，才写入内存。嵌套 TLS 对象会需要独立复制；只选 TLS 的输入不能被 string-only 检测吞掉。
- `internal/httpapi/database_connection.go:135–139` 也只检查四个 string 字段。新增 TLS 时，必须把 TLS-only 输入识别为手动策略请求并明确处理，而不是悄悄取用旧凭据。
- `frontend/src/api/databaseConnectionResponse.ts` 已区分凭据保存结果未知和“连接已确认但目录失败”。TLS 失败/能力不足不能破坏这套状态，不能自动重发密码。

### 4. RPC 与 HTTP 的差别

- HTTP `internal/httpapi/database.go:168–197` 使用 `DisallowUnknownFields`：旧 HTTP Server 收到未知 TLS 字段会拒绝。
- Agent `internal/agentsocket/database_service.go` 的 `decodeDatabaseRequest` 使用普通 `json.Unmarshal`：旧 Agent 会忽略未知 TLS 字段。普通成功诊断没有策略确认字段。
- 现有 `QueryWithOptions` 是正确的局部先例：新语义走单独路径，旧 Agent Unimplemented 不降级、不重放（`database_client.go:77–95`，`database_rpc.go:14–23,161–167`）。TLS 必须保护所有实际建连入口，而不是仅 TestConnection。

## 二、已安装驱动的精确语义

以下结论来自当前 pinned dependency 源码，不是推测线上服务设置：

### MySQL v1.10.0

依赖模块：`github.com/go-sql-driver/mysql@v1.10.0`（依据当前锁定模块源码）。

- `dsn.go:196–221`：空/false 的 TLSConfig 不建 TLS；true 建正常校验 TLS；skip-verify 设置 InsecureSkipVerify；preferred 同时允许明文 fallback。正常校验时 ServerName 缺省从 Addr 的 host 取得。
- `packets.go:219–225`：服务器没有 SSL capability 时，允许 fallback 才清空 TLS，否则 ErrNoTLS。严格策略必须令 AllowFallbackToPlaintext=false。
- `dsn.go:368–370`：FormatDSN 只写 TLSConfig 的注册名，不序列化 Config.TLS。直接设置 Config.TLS 再 FormatDSN 会丢配置。
- `driver.go:100–109`：NewConnector 会 Clone + normalize。用 `mysql.NewConnector` + `sql.OpenDB` 可以绑定每次请求的 TLS，不需要全局 RegisterTLSConfig/DeregisterTLSConfig，也避免命名冲突/并发回收风险。
- literal Config→FormatDSN→ParseDSN 的现有行为不能随手替换为 NewConfig 默认：AllowNativePasswords/CheckConnLiveness 等缺省不同。严格路径可先对现有生成 DSN 做 ParseDSN 取得等效非 TLS 配置，然后显式设置 TLS/Fallback；旧 nil 策略继续旧路径，避免顺带改鉴权行为。

### pgx v5.10.0

依赖模块：`github.com/jackc/pgx/v5@v5.10.0`（依据当前锁定模块源码）。

- `pgconn/config.go:283–303,833–886,955–963`：prefer/allow 用 Fallbacks 实现明文回退；require 在没有 CA 时跳过证书验证，有 CA 时按 verify-ca（仍不校验服务器名）；只有 verify-full 做完整服务器身份校验。仅赋值主 TLSConfig 不会删除原 Fallbacks。
- `pgconn/config.go:803–815`：sslrootcert=system 会调用系统 CA 并把 sslmode 改为 verify-full，发生在 disable 分支之前。因此不能把当前 sslmode=disable 的 DSN 文本当作运行时明文证明。
- `pgconn/config.go:323–364,486–498,579–618` 及 `pgconn/defaults.go`：解析合并默认/环境/输入，可能读 service、passfile、root/client cert/key。ConnStringAllowedKeys 只限制连接串字段，不隔离 PG* 或默认文件。
- `pgconn/config.go:32–33`、`conn.go:165–221`：必须由 ParseConfig 建立带私有标记的配置，不能手写 pgx.ConnConfig 绕开解析。`conn.go:211–217` 还会把输入种子字符串保留到私有 connString，可由 ConnString() 取得；所以解析前就不能把真实密码/Token 放到种子字符串，单纯之后清理错误链不够。
- `stdlib/sql.go:195–205,228–230` 支持 `GetConnector`/`OpenDB`，不需要全局 RegisterConnConfig；严格连接可用解析出来的新 config + 独立 tls.Config。

## 三、拟定策略合同（新增设计，不是已实现行为）

1. 内部可选 policy：nil 表示没有新增 TLS 意图，保留原行为；非 nil 必须 `mode="verify-full"`。空对象、空 mode 和其他模式拒绝，绝不把拼写错误归零。
2. `caPEM` 缺省：Root Agent 系统根证书，由 Go TLS 在握手时使用。编译器留 RootCAs=nil，不调用 SystemCertPool，不作文件 I/O。
3. `caPEM` 有值：只信任这批证书，不暗中合并系统根。预算 64 KiB、最多 32 个 CERTIFICATE PEM block；严格拒绝无证书、坏 DER、私钥/其他 block、PEM headers，以及起始/块间/结尾的非空白垃圾。逐块完整验证，不能让 pem.Decode 跳过前面损坏或格式无效的 block 后接受后续证书。每张证书必须 BasicConstraintsValid=true、IsCA=true，且有可用的证书签名 KeyUsage；不要求 CA 自签名，非自签名 CA 也可作为明确提供的 trust anchor。KeyUsage extension（OID 2.5.29.15）缺省时按 X.509 无限制处理，允许；如果 extension 存在，则必须含 KeyUsageCertSign，存在但解码值为零也拒绝。不能仅凭 KeyUsage==0 就认定 extension 缺省。只允许 PEM 内容，不接受 URL/Agent 文件路径。
4. ServerName 缺省来自实际拨号 host；显式 override 只改变待校验身份/SNI，不改变连接目的地。用于 loopback/发布端口与证书 DNS SAN 不一致的情况。T0 接受 raw IPv4/raw IPv6，初版明确拒绝 bracketed IPv6 和 zone。DNS 只接受 ASCII LDH label，每 label 1–63 字节、规范化后总长不超过 253 字节，label 首尾不得为连字符；统一小写，允许并仅删除一个终止点，双终止点/空 label 拒绝。国际域名需 A-label。拒绝 URL、路径、host:port、通配符、下划线、控制字符、内部/首尾空白，不能靠 TrimSpace 把非法名字变为合法。IP 按解析结果规范化；最终身份是 IP 时必须验证 IP SAN，不把数字形式经字符串处理后当 DNS 校验。显式空 serverName 等同未提供，只在非空时执行 override 校验；不把非法非空名字当作缺省。
5. `tls.Config{MinVersion: TLS1.2, InsecureSkipVerify:false}`，不设置跳过验证回调，不允许 plaintext fallback。每次编译新 config/CertPool，不共享可变对象。
6. 仅 MySQL/PostgreSQL 可选 policy；SQLite 非 nil policy 明确拒绝。nil SQLite 完全不变。
7. 编译错误是固定安全错误，不能带 PEM、服务器输入、DSN、密码或 raw driver error。UI 不展示 CA 原文或假称发现/缓存目录证明加密。
8. 现有保存记录不自动改写、不自动开启 TLS。用户明确启用后，策略与凭据共同测试、共同保存。已有显式 TLS 记录被省略 policy 的手动旧客户端请求使用时，新 Server 应保留原 TLS 策略或要求明确策略，不得省略即覆盖为 legacy。初版不提供降级/关闭 TLS 的新控制。

## 四、分阶段代码与验收

### T0：纯 TLS 策略编译器（推荐立即实施的第一片）

范围只限 `internal/database/tls_policy.go` 和对应测试，可有一个包内策略类型；**此阶段不要给 Credentials 增加 JSON 字段，不接 Manager、RPC、vault、HTTP 或 UI**。函数输入 Driver/拨号 host/可选 policy，返回独占 `*tls.Config` 或固定错误。它将直接被 T1 的 MySQL/pgx adapter 和 T2 的输入/保存验证复用，不做第二套验证器。

验收：

- nil 对所有现有 driver 返回“沿用原路径”结果；verify-full 只接受两个网络 driver。
- 模式大小写/空对象/未知模式明确拒绝，不返回 nil success。
- 空 CA 返回系统信任配置；一个/多个合格 CA 返回独立 pool；64 KiB 边界、32 证书边界、无效 PEM/DER/私钥/headers、起始/块间/尾随垃圾及“前一 block 损坏但后一 block 有效”全部拒绝。BasicConstraints 无效、IsCA=false、KeyUsage 禁止签名的证书拒绝；非自签名合格 CA 接受。分别构造 KeyUsage extension 完全缺省（允许）、存在且含 CertSign（允许）、存在但值零（拒绝）、存在但只有 DigitalSignature（拒绝）的四类 fixture；依据 OID 2.5.29.15 是否存在区分缺省与零值。
- DNS/raw IPv4/raw IPv6 及显式 serverName 覆盖；loopback host + DNS override 不改变 host 输入。括号 IPv6、zone、URL、端口、wildcard、下划线、控制/空白、空 label、首尾连字符拒绝；label 63/64、总长 253/254、大小写、单终止点与双终止点边界逐项冻结。结果规范化一致，同一输入在默认 host/显式 override 两条路径不产生不同身份语义。
- MinVersion=1.2、InsecureSkipVerify=false、无自定义验证绕过回调。
- 两次编译 config/RootCAs 不别名；原 policy 改动不能影响已编译配置。
- 内存生成测试 CA/leaf，用 x509.Verify 证明受信 CA/正确 DNS SAN、正确 IPv4/IPv6 SAN 通过；错误 SAN/未知 CA/过期 leaf、只有 CN 无 SAN、只有 DNS SAN 却用 IP 身份全部失败。custom CA pool 的独占信任必须有证据：配置 A 只通过 A 的链，另一独立 B 根签发的同名证书失败，不依赖测试机器的系统根集合。不做真实握手或 listener；这只证明配置和证书验证逻辑，不能宣称 DB 集成通过。
- 建议精确 `go test ./internal/database -run 'TestCompileDatabaseTLS'`、对应 race、Go build/vet 与现有 CI；不因本片没有界面变更增加虚假视觉验收。

退出：首片独立审查通过、文档明确“尚未提供用户 TLS 功能”；随后优先接续 T1/T2，不无限停留在未接入的基础代码。

### T1：每次连接的严格 driver adapter

新增只给非 nil policy 使用的连接构造分支；nil 完整保留现有 connectionString→sql.Open 路径和池/关闭/context 行为。严格 adapter 自身也必须拒绝 nil/无效 policy，不能因为调用者漏传而调用编译器的 legacy 返回分支。MySQL 按上节 NewConnector；pgx 按下一节精确环境隔离，配置 Fallbacks=nil、TLSConfig=编译结果、固定 postgres SSL negotiation。禁止记录/返回 DSN，禁止全局注册 TLS profile。pgx 解析种子不含实际密码/Token；最终 config 必须持有真实 Password 字段才能连接，不能声称零凭据内存或零连接串内存，具体保留边界见下节。

此片尚不开放 HTTP 字段；只做包内可调用构造与 fake opener 集成测试，确认无 policy 时旧 source/DSN/错误行为未变，严格配置不会退回旧 builder。构造错误发生在任何网络拨号之前。Fake 必须确认池生命周期/错误关闭不回归。

### T2：保存兼容性与 fail-closed RPC（必须在 UI/API 开放前完成）

可拆成两个小 commit，但必须完成整个门槛再开放功能：

- policy 置于连接凭据 payload 内，让自动连接/后续所有操作复用；不要放到发现 Source 的全局可变字段，不改 sourceID/endpoint 含义。
- v1 继续按原 AAD 解密/轮换；有明确 TLS 的记录使用 envelope v2。v2 使用版本化 AAD（保留原 source/driver/endpoint绑定并加入不可省略的版本域）；不仅是外层 Version=2。只有 v2 可含非 nil TLS policy，且 v2 必须含非 nil、完整有效的 verify-full policy。v2 解密后的 payload 缺少 tls、tls=null、tls={}、空/未知 mode 或无效 CA/身份一律为 credential_corrupt；绝不把它交给编译器的 nil legacy 分支。当前不加 SQLite 表列，也不批量迁移旧记录。
- Save/Resolve/manual test、seal/open、rotation 两条加密路径都保留 policy；复制嵌套 policy。测试成功才保存，测试失败不改已保存值。v2 回滚到旧 Server 会被拒绝并关闭自动连接，需明确说明恢复升级后可能要手动重连；绝不能自动生成一个 v1 无 TLS 副本。
- 省略 policy 的旧请求遇到已有 v2 记录，要在**测试前**的新 Server 中继承已有严格策略或固定拒绝；不要在替换密码时丢失 TLS。冻结此次有效策略与保存记录的并发基线，再按该冻结策略测试。`/test-connection` 的直接 Agent 路径也必须纳入这个规则；保留 test-only 不写 vault 的语义。测试完成后不得再次继承新出现的严格策略，再把此前的 legacy/明文测试标为该策略测试通过。
- **保存时的反降级竞争保护是 T2 的强制门槛。** 测试前捕获记录存在/不存在及精确并发 token；测试成功后通过原子 CAS，或同一短事务内重新读取、比较、写入，保证目标仍是同一基线后才保存。不能用“先 GET 再无条件 Put”、仅秒级 updated_at 或仅比较 mode。可在不加表列的前提下比较预期存在性、ciphertext、keyVersion、driver/endpoint 及相关自动启用状态；新建用不存在条件，已有记录用完整匹配条件。事务不跨网络测试。目标变化则固定冲突失败、本次不保存、不自动重测/重放密码；尤其禁止在 CAS 失败后把新 strict policy 拼到未按它测试的凭据上重试保存。新代码的所有凭据替换入口必须经过同一守卫。
- 轮换同样不得用旧 v1 快照覆盖并发的新 v2：ApplyRotation 对各记录带入预期密文基线并原子比较，冲突时保留当前密文、按明确失败路径处理；不能把旧快照重新加密后无条件写回。此 CAS 保证范围是新代码的受控写入；部署阶段必须避免旧版本写进程与新版共享同一 vault 并发写入，本提案不执行部署调整。
- TLS 请求走一个新数据库 service namespace（沿用当前 per-operation request/response，不新增通用 exec），或等价的每操作 WithTLSPolicy 方法。推荐只新增第二组严格 service 路径，并重用现有 handlers；注册严格 service 必须要求 provider 明确支持 TLS，不能把任意旧 provider 挂上去。
- **每个严格 RPC handler 必须独立验证非 nil、完整有效的 verify-full policy。** credentials/tls 缺失、null、空对象、空/未知 mode、坏 CA/身份均在 provider 或拨号调用前固定拒绝；不能只依赖新客户端会发送正确字段，也不能调用编译器的 nil legacy 成功分支。专用 service 路径是安全语义承诺，不能成为第二个兼容旧默认的入口。
- TestConnection、Catalog、Query/QueryWithOptions、Rows、Insert、Update、Delete 全部受保护；旧 nil 请求仍走旧路径。Unimplemented/unsupported 在新路径直接变为稳定 `tls_policy_unsupported`，不能清除字段重试旧路径。仅先查 capabilities 再调旧方法不足以保证真实执行时不降级，不采用这种方案。
- SQL resultMode 与 TLS 组合要同时保留两套语义，特别是显式 rows/exec 不得重新退回普通 Query。

无监听器测试：fake grpc.ClientConnInterface 记录 method/payload；旧 Agent fake 只支持旧路径，严格请求只调用新路径一次，mutation provider 调用为零；直接调用严格 handler/fake provider 做所有操作矩阵。加现有 Struct round-trip fixture，不要求 bufconn/listener。严格 handler 对所有操作做缺失/null/空/非法 policy 矩阵，provider 调用数必须为零；有效 verify-full 只进入严格 adapter。v2 合法加密但 payload 缺失/null/空/非法 policy 的固定夹具应在解密校验处失败，tester/driver 调用为零。

### T3：HTTP/OpenAPI/表单闭环

- T0/T1/T2 门槛齐备后才在 Credentials/API/OpenAPI 暴露可选 TLS 对象。新增固定 `tls_configuration_invalid`、`tls_policy_unsupported`；证书错误用类型检查分类为 `tls_verification_failed`，不依赖原始错误文本。具体 status/安全提示同步 HTTP/RPC/前端已知码及契约测试。
- 修改 HTTP/Pinia 的 credential presence 与深复制；TLS-only 输入明确处理：要求本次用户名/数据库，或显式定义继承保存凭据的流程，不能靠 string-only 检查悄悄变 auto。初版建议要求完整手动连接意图，避免增加“只改部分凭据”API。
- 手动表单新增一个“启用证书校验 TLS”的显式入口；下面选择系统 CA 或 PEM CA，提供可选服务器名。旧/默认表单不自动切换模式。不提供不校验证书、优先 TLS 或关闭已保存 TLS 的新选项。
- 自动使用已保存 TLS 时，表单文案说明未提供覆盖设置将沿用保存策略。缺少安全状态的旧响应只展示“未确认”，不得显示已加密。
- 成功诊断可增加安全的已应用策略摘要（mode、serverName、system/custom trust）；不返回 CA 原文/密码/DSN，不把“请求了 TLS”当成“连接成功”。需要实际握手版本/套件时另加驱动级采样，本片不捏造。
- 保留 source/session/request owner 隔离。发请求前深复制 TLS；测试中改 CA、切来源、晚到响应不能改变当前草稿/结果；成功连接后目录失败仍只报目录失败；保存结果未知不自动重试。

验收：Go 编译/vet/定向 fake/race、OpenAPI兼容夹具；前端 policy 草稿/校验/序列化/旧响应/错误归类单元测试；既有 typecheck/lint/build。浏览器 fixture 在 390/1440 和大字号下检查 CA 输入、折叠项、焦点与固定错误提示，实际 PNG/axe 验证，明确不是数据库真实握手。

### T4：受控集成/交付，不包含部署

在另获明确允许的隔离环境跑 MySQL/PostgreSQL TLS fixture，分别验证系统/自签 CA、正确/错误 SAN、过期证书、不支持 TLS、Agent 版本不匹配、保存后自动查询。也可以先完成纯/fake 交付但必须保留“真实数据库握手未验”。不启动监听器、不改 NAS/证书/Agent环境作为本次规划或 T0 的副作用。源码 commit/push 和 CI 不等于开启用户 NAS 的 TLS。

## 五、pgx 环境隔离：明确区分兼容与新增安全语义

**Legacy nil：原样保留。** 继续现有 URL 与 sql.Open，包含既有 PG* / 默认文件行为。不能在 TLS 首片顺带清环境、改旧用户鉴权或禁止 service；也不能把它称为确定明文。未开启新策略的部署行为不变。

**显式 verify-full：独立、受控构造。** pgx v5.10.0 没有忽略环境/默认设置的 ParseConfig 选项；不能把 ParseConfig 或 ConnStringAllowedKeys 说成纯函数或环境隔离。

建议的具体实现：

1. 不修改进程环境；不在请求内 Setenv/Unsetenv，不复制/读取真实用户证书内容。通过可注入的小 env-presence 读取器，发现非空 PGSERVICE 时在解析前固定失败为配置错误，说明此显式策略暂不支持 Agent 的 service 环境配置。nil 路径不受该限制。
2. 严格分支产生受控结构性种子 URL，host/port/user/database 明确，**password 必须明确为空**（例如 url.UserPassword(username, "")），并同时带 passfile=；不要在种子中放真实密码或 Token。显式空值覆盖 PGPASSWORD，空 passfile 阻止从默认/环境文件补入密码。只有 ParseConfig 成功后才给 config.Password 赋 credentialSecret 的真实值。种子限定单个网络 host，拒绝逗号多 host、Unix socket 路径。设置 sslmode=disable 作为“先不让驱动从环境构建 TLS”的解析种子，而不是最终拨号模式。
3. 在种子 URL 中明确 `sslrootcert=`, `sslcert=`, `sslkey=`, `sslpassword=`, `passfile=`, `servicefile=`，覆盖环境及默认文件路径。设置 `sslsni=1`, `sslnegotiation=postgres`, `connect_timeout=0`, `target_session_attrs=any`, `min_protocol_version=3.0`, `max_protocol_version=3.0`, `channel_binding=prefer`, `require_auth=`，冻结 pinned 版本的其他连接/安全默认。PGSERVICE 不添加为空的 URL 键，因为驱动按键存在就试读 service；前述 guard 负责明确拒绝它。servicefile 空值还使 guard 与解析之间任何异常环境变化失败，而不读取另一个路径。
4. ParseConfigWithOptions 仅允许这些程序生成的字段，防止日后输入变成任意 driver option；不是允许用户输入 DSN。成功解析后覆盖精确 Host/Port/User/Database，再将真实凭据写入 config.Password，清 RuntimeParams（不继承 PGAPPNAME/PGTZ/PGOPTIONS），设 Fallbacks=nil、TLSConfig=编译器配置、SSLNegotiation=postgres。pgx 仍保留经过脱敏的结构性种子 connString，ConnString() 不代表最终 Password/TLSConfig；不得打印/返回它，也不得把它再交给 sql.Open，否则会丢掉后赋的真实密码与严格 TLS。受控 sslmode=disable 只存在于解析种子；最终必须经配置对象 connector 打开，且严格 adapter 在交给 connector 前断言 TLSConfig 非 nil/验证开启/无 fallback。
5. 此方案仍会调用 pgx defaultSettings 的 user lookup/已知路径 Stat，以及 ReadPassfile("") 的失败 open；不是“零文件系统 I/O”。它不读 inherited service/passfile/CA/client 私钥内容，也不接纳这些来源的策略。T0 不调用它，保持真正纯函数。不要为了伪装纯函数复制 pgx 私有配置标记或 fork 驱动。
6. 任何 parser 错误只转固定安全码，不在错误链保留含 DSN 的 ParseConfigError；不自动改成 legacy。现有 Manager 有 Cause 链，应在新增严格 adapter 的出错边界主动截断敏感解析错误。

固定验收矩阵：用测试进程 t.Setenv 和已知假字符串/临时 fake 文件（不使用真实凭据）逐项设 PGSSLMODE=prefer/allow、PGSSLROOTCERT=system/无效路径、PGSSLCERT/KEY、PGPASSFILE、PGPASSWORD、PGSERVICE/SERVICEFILE、PGSSLNEGOTIATION=direct、PGHOST、PGPORT、PGDATABASE、PGUSER、PGOPTIONS/TZ/APPNAME。除显式 PGSERVICE 固定拒绝外，verify-full 最终 Host/认证字段/CA/ServerName/Fallbacks/RuntimeParams 与环境为空时一致。各例证明未调用拨号/未返回 sentinel。另用 fake 真实密码/Token sentinel 验证：parser 接收的种子与最终 config.ConnString() 均不含该 sentinel；parser 成功后 config.Password 精确等于该 sentinel；PGPASSWORD 的假环境 sentinel 也不进入种子/最终认证字段；parser 失败时根本没接触真实密码。不能把这些测试解释为最终 config 不含凭据，它必须保有 Password。legacy 分支则确认仍使用旧构造，原环境兼容不被改变。不能把清理测试进程环境的做法复制到运行时。

## 六、迁移、轮换与回滚必须通过的测试

1. 固定旧 v1 密文 fixture：新代码解密四字段完全相同、nil policy、source/driver/endpoint AAD 完全不变；不写回、不升级版本。旧 nil 请求所有操作仍走旧 RPC。
2. v2 round-trip：模式、CA PEM、serverName 原样进入加密 payload，Save→Resolve→Agent requests 保留；明文 metadata/JSON诊断/错误不含 PEM/secret。
3. v1/v2 混合轮换：更换 fake memory key 后两类记录均可恢复；v2 不变回 v1；nil 记录继续兼容。模拟一条失败保持原密文并沿用已有 fail-closed 自动禁用语义。
4. AAD/篡改：source/driver/endpoint 不匹配失败；把 v2 envelope.version 改为1也解不开（因为 AAD 版本绑定）；篡改 policy bytes、未知 mode/未知 envelope version 明确失败。
5. 旧解密器 fixture 拒绝 v2，且绝不发出 Agent 建连；记录降级失败后自动禁用的现有行为，并说明重新升级不会凭空恢复 auto 标记。测试只 fake store，不修改实际凭据。
6. 新 Server→旧 Agent：手动测试/目录/SQL/行增删改全部固定 unsupported，最多一次新 RPC，零旧 RPC/零写 provider 调用；旧 nil 请求仍兼容旧 Agent。
7. 新 Server 替换已保存 v2 的用户名/密码时省略 TLS：在测试前保留已有严格 policy 或请求明确拒绝；失败测试不覆盖旧值。绝不把 omitted policy 当用户授权降级。
8. 回滚边界要诚实：旧二进制直接接受用户重新手动输入的 legacy 凭据这一既有功能无法由新 envelope 彻底封锁；保证范围是 TLS-bearing 保存记录不会自动静默退化，不能声称旧 Server 所有任意手动调用都被新代码保护。
9. HTTP 旧 Server 对新增 TLS 字段拒绝、前端不自动剥字段重试；连接保存后的未知 HTTP/网络结果仍只显示未知，不重发凭据。
10. **纯并发交错（必过）**：fake vault + 可暂停 tester，让 A 读取 v1/无记录并开始 legacy 测试；B 完成 verify-full 测试并保存 v2；再放行 A 的旧测试成功。A 必须在最终 CAS 固定冲突，B 的密文/policy/自动状态完整保留；A 不第二次拨号、不继承 B 的策略后伪称验证过、不发无条件 Put。再覆盖 A/B 同秒、B 删除后重建、B 轮换/改 CA/改 serverName、两个新建操作和 CAS 比较到写入间竞争。测试使用内存 fake 和可控 barrier，无真实凭据、监听器或数据库连接。
11. 并发 token 在同一原子操作内检查；另外用 fake 轮换验证“读取旧 v1→并发保存 v2→迟到轮换回写”也不得恢复 v1。确定 CAS 冲突可以给出“本次凭据未保存，请重新确认”的固定错误；持久化超时/提交结果未知仍不能归为确定未保存，也不自动重放。
12. 严格 RPC 和 v2 decoder 的不变量与 T0 nil 兼容语义分开测试：缺失/null/空 policy 在严格边界没有任何 legacy 降级通路；nil 只在明确旧入口/v1 凭据路径被允许。

## 七、未验证项和不可扩大范围

设计依据是源码/依赖语义审查；各阶段验收是实施要求，只有下方实现记录明确列出的检查已经通过。真实 TLS 数据库连接未验证。没有读取实际数据库密码、客户端私钥或运行 PG* 内容。未处理全站 HTTP TLS、Agent socket 身份、mTLS 客户端证书、证书上传到主机、全局 CA 安装、任意 DSN 编辑器、外部数据库发现扩大、连接池重构或用户 NAS 部署。这些都不应借 TLS 基础片顺带执行。


## 八、T0 实现检查点（2026-10-14 06:41 UTC）

包内 `tls_policy.go` 与纯测试已实现，未接入 Credentials、Manager、HTTP、RPC、vault 或 UI。nil 仅保留旧入口语义；显式配置只编译 verify-full。三个新生产函数语句覆盖率100%，不代表完整安全性证明。Go 数据库完整单元、TLS定向race、全仓构建/vet通过；纯证书验证覆盖CA/SAN/过期/EKU与原始字节/证书数边界。没有真实握手、证书安装、生产变更或NAS操作。

T1连接器、T2保存与协议防降级、T3用户界面仍未完成。现有数据库连接不会因本片自动启用TLS，不能报告为“数据库TLS功能已上线”。优先接续这些门槛，并保留T4受控集成未验的标记。

## 九、T1a 严格连接器构造检查点（2026-10-14 06:54 UTC）

`tls_connector.go` 新增包内连接器构造函数，严格拒绝nil策略，复用T0配置。MySQL使用每实例NewConnector，保留既有DSN解析后的非TLS默认选项；pgx使用空密码受控解析种子、清除回退/运行参数并最后赋值实际认证信息。无全局注册，无进程环境修改。只构造connector，不调用Connect或Ping。

纯测试覆盖单个及组合PG环境污染、PGSERVICE在检查后变化、种子/错误不含密码、解析器配置复制、独立CA池和明确host与验证名。定向及race、完整database测试、全仓构建/vet通过。实际解析仍有前述用户/默认路径Stat和空passfile的失败open，不把它描述为T0式纯函数。

本片仍未接入Manager与sql.OpenDB池生命周期，称为T1a构造基础；T1b运行分派和池释放测试必须与T2严格RPC/vault门槛共同完成，之后才可开放HTTP/UI。旧连接路径原样保留，真实数据库TLS握手未验。T0的3ea4264已完整CI通过3266前端单元/407浏览器及Go/Compose，不证明本片连接器的实际握手。

## 十、T2 并发存储方案修订与 A0（2026-10-14 07:10 UTC）

[精确存储与RPC分阶段方案](NCP-2026-10-14-database-tls-vault-rpc.md)补充完整ABA要求：仅比较时间或密文无法识别“无记录→新建→删除→仍无记录”。因此增加独立保留revision/删除墓碑表及触发器，原凭据表列、密文和旧v1内容不批量迁移。单纯时间戳更新不推进revision，策略/密文/自动状态变化推进；整数溢出整条写入失败关闭。

A0新增一致快照及SQLite条件保存/禁用/原子轮换原语，18组临时SQLite回归、完整database/race/build/vet和独立审查通过。现有生产接口/调用仍未切换，A1负责测试前快照、全部写入与HTTP409闭环；不将原语存在说成并发保护已启用。T1a的6f4a7e9完整CI通过3266单元/407浏览器及Go/Compose，仍为未接入的TLS基础。

## 十一、T2-A1 并发保存保护代码接入（2026-10-14 07:28 UTC）

ManualConnect已在测试前冻结revision并条件保存，Resolve与轮换也切到原始快照条件写入。409只表示本次凭据未保存，保留草稿并禁止自动重试；持久化提交未知仍按503/结果未知处理。移除旧无条件写入口，轮换冲突不撤销已发布key，版本溢出/重用失败关闭。详见专项A1记录与测试证据。此处描述仓库代码接入，不表示生产已部署；TLS运行入口、v2和严格RPC仍未开放。
