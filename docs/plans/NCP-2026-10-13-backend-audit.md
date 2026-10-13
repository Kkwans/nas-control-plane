# NCP 后端与架构只读审计

日期：2026-10-13。范围：当前 checkout 的 cmd、internal、部署连接点。已阅读 AGENTS.md；未读取 OpenSpec 工作流；未修改仓库、未运行 Agent、未连接 NAS、未执行生产操作。以下是静态代码证据，不声称已完成运行复现、Go 编译或竞态测试。P0 表示应优先阻断安全/可靠性交付风险；P1 表示紧接着交付的可靠性和体验基础。

## 结论

这是已有真实垂直切片的 Root 控制台，不应重写成另一套 CRUD 项目。HTTP Server → Unix gRPC → root Agent 的职责方向正确，Docker、PTY、Compose、数据库访问主要在 Agent 中实现；Server 管认证、配置持久化和 API。优先投入应是安全边界、Job 生命周期、流式资源回收、Compose 变更事务性，然后才是新模块扩张。当前“强类型 RPC”的准确描述是：业务 Go 请求类型 + 手写 gRPC service + Struct 动态消息，还不是 schema-first protobuf 契约。

## 可保留、可复用的资产

- Agent provider 接口可注入，HTTP 多个 AgentClient 分面接口便于隔离验证：internal/agentsocket/server.go:38–56、182–197；internal/httpapi/handler.go:42–136。真实能力与 UI 框架基本分离。
- 会话使用 32-byte 随机 token，数据库仅保存 hash；bcrypt 密码、禁用用户和改密清理会话已实现：internal/auth/service.go:23–24、191–224、374–407、465–490。不要误报“明文会话”或“没有改密失效”。
- 数据库凭据是独立部署密钥 + AES-256-GCM 的现有 vault，不宜另造密码保存系统：internal/database/crypto.go:19–23、32–40；cmd/ncp-server/main.go:159–185。自动连接与运行 SQL 的连接解析已有专门 coordinator。
- 数据库读结果对 bigint > JS 安全整数、精确 decimal 已做字符串保护：internal/database/manager.go:774–837；CRUD 有 schema 元信息、主键谓词、参数占位符：337–421、702–736。
- Root terminal 是独立 PTY/gRPC 双向流，不是 shell 字符串拼接 HTTP exec：internal/httpapi/terminal.go:57–80；internal/agentsocket/terminal_service.go:35–39；internal/terminal/host_linux.go:68–91。WebSocket 使用库默认 Origin 校验，nginx 保留浏览器端口：deploy/console/nginx.conf:68–80。
- 容器 action 后的结果/检查、Compose lifecycle 的 inspect 匹配、数据库错误码映射、请求关联日志、图标 proxy 的 SVG sandbox 均可复用。尤其 proxy 已有 CSP，可以修复上传图标时统一而不是新增另一套安全规则。

## P0：确定代码缺陷和直接边界风险

### B01 Job 共享 map 数据竞争，最坏可致 Server 崩溃

证据：internal/httpapi/jobs.go:179 原地修改 job.Layers；234–238 get 仅复制 struct，仍返回同一个 map；list 同理；278–282 persist 在锁外 json.Marshal；94–110 update 另起 goroutine persist。HTTP JSON 和 SSE JSON 编码也在 registry 锁外。

后果：拉取进度持续更新时，轮询/SSE/异步落库可能同时遍历和写同一 map。不是“已经观察到崩溃”，而是明确的共享可变对象竞争；Go 的 concurrent map iteration/write 可直接终止进程，影响所有控制台功能。

独立实施：创建不可变 snapshot/deep clone，在锁内复制 Layers；所有出 registry 的快照含嵌套对象都脱离内部状态。验证：纯 fake 的 progress/get/list/marshal/persist 并发测试并运行 race；不需要 Docker/NAS。

### B02 Agent socket 将 root 能力授予宽泛 users 组

证据：deploy/systemd/ncp-agent.service:8–9 为 root，19 使用 --socket-group users；internal/agentsocket/server.go:31–32 默认为 0660/0750，applySocketPermissions 将 socket 与目录赋给该组；182 使用无身份 interceptor 的 grpc.NewServer，后续注册包括 Docker/文件/终端/系统 RPC。

结论：具备 socket 路径访问且属于 users 组的本地进程可绕过 Web 登录直接调用 root 能力。实际 users 组成员及容器挂载须部署审计确认；代码本身没有区分“ncp-server”与“其他 users 组应用”。这不是指 Root 网页用户不应有 root 权限，而是进程边界过宽。

独立实施：专用 ncp-agent-client 组，仅授予 server 身份；必要时 Unix peer credential UID/GID allowlist；严格验证 socket 父目录所有者，保留非 TCP 边界。验证：隔离环境用授权/未授权身份对临时 socket 访问，既验证拒绝又验证正常 server 可连。

### B03 上传 SVG 同源作为文档时可执行脚本

证据：internal/httpapi/site_icons.go:58–64 用头部 <svg 判定接受 SVG，81–83 原样写入；107–108 仅 Cache-Control + http.ServeFile，无 CSP。对照同文件170–172，远程图标代理专门设置了 CSP sandbox，上传读取分支遗漏。deploy/console/nginx.conf:19–21 无全局 CSP，不能补齐。

利用前提：不可信 SVG 被上传且用户把该图标 URL 作为文档打开（普通 img 嵌入通常不会执行脚本）；随后脚本位于 Root 控制台同源，可调用带 Cookie 的管理 API，HttpOnly 无法阻止同源操作。

独立实施：所有图标响应共用安全 writer，SVG 至少 CSP sandbox + default-src none；更强选项为可靠净化/栅格化或隔离无凭据域。验证：含 script/onload/foreignObject/external reference 的无害 fixture，在独立浏览器测试文档导航与 img，确认不能执行任何同源 API 请求。

### B04 HTTP 写接口缺少跨源防护，同站不同端口不被 SameSite 阻断

证据：internal/httpapi/handler.go:262 后仅 request-ID 与认证中间件；internal/httpapi/auth.go:164–180 使用 SameSite=Lax；internal/httpapi/docker_handlers.go:33–50 的容器操作无需 body，可直接 POST；没有 Origin/Referer/CSRF/Fetch-Metadata 校验。整个 auth/httpapi 搜索未见 rate-limit/CSRF 中间件。WebSocket 的默认 Origin 校验不覆盖普通 HTTP。

风险条件：NAS 常在同一 IP/hostname 的其他端口托管应用。恶意/被攻陷的同站不同端口页面可发送带凭据的简单跨源 POST；SameSite 不按端口隔离，CORS 只阻止读取响应、不保证阻止写入。跨站互联网攻击不能据此一概判定成功，需分别验证浏览器站点语义。

独立实施：统一不安全方法 Origin/CSRF 防护，明确 trusted reverse proxy/host；JSON 输入检查 Content-Type，bootstrap/login 也纳入；nginx API 的 Host 当前是 $host（不含端口），实施 Origin 比较时必须同时修正/规范外部 origin。验证：同源成功，同 hostname 不同 port、跨站、Origin null 均拒绝，命令行受支持认证流程明确。

## P1：任务、会话与恢复可靠性

### B05 Job 状态落库乱序、错误丢弃和重启恢复不可信【确定】

证据：jobs.go:110 每次状态变更无序 go persist；278–292 忽略 UpsertJob 错误；controlstore/store.go:683–707 无条件 UPSERT，无 revision/CAS；jobs.go:74–79 忽略 interrupted 更新及加载失败；删除见255–274。

后果：running 的旧持久化可在 completed 之后执行；删除后迟到 UPSERT 可复活记录；存储满/锁冲突仍向用户返回 accepted/completed。内存只恢复最近500条，运行期间则无上限。不是持久 job queue，没有“任务已可靠保存”的承诺依据。

独立实施：独立 Job engine + 顺序事件/单 writer 或 DB revision CAS；create 持久化成功才 accept；取消与完成用状态机；失败可观测、墓碑/版本防复活；列表真正分页/保留策略。验证：可控阻塞 fake store 反转写入顺序、删除后迟到写、磁盘写失败、重启加载失败。

### B06 Cancel 是乐观改终态，并未等执行停止【确定】

证据：jobs.go:125–151 有 cancel func 就先标 cancelled，随后 cancel()；docker_images.go:62–76 在异步 goroutine 才注册 cancel，故创建后立即取消有窗口失败；jobs.go:94–110 update 不检查终态；docker_images.go:107–108 成功路径仍 complete；队列 slot 与 ctx.Done 同时 ready 时可先选择 slot 并改 running。Compose job（httpapi/compose.go:102–132）没有注册 cancel。

后果：“下载已停止”可能早于 Agent/Docker 确认；取消与成功竞态可覆盖终态；统一 cancel API 实际只部分任务可用。HTTP 断线不应取消所有 Job，但用户取消应是明确生命周期。

独立实施：创建时安装上下文、cancelling 中间态、worker 完成后确认 cancelled；终态单调；每种 job 宣告可取消性，Docker 实际副作用停止/保留产物语义明确；排队 deadline 与执行 deadline 分离。验证：立即取消、排队取消、最后回调时取消、超时、重复取消、Agent断线。

### B07 会话撤销未关闭已建立的 Root terminal/SSE【确定】

证据：auth.go:123–144 仅请求入站 Authenticate；auth/service.go:215–224 logout 删除 DB session；httpapi/terminal.go:57 只使用 request.Context + terminalTimeout（默认30分钟，handler.go:28）；systemEvents 455–518 只观察请求 context。没有 user/session revocation 到活动流的登记/取消链。

后果：禁用用户、改密、退出后已建立的 root shell 仍可继续运行到断线/超时；SSE 可继续读状态。普通新 HTTP 请求确实会被拒绝，不要混为会话完全无效。

独立实施：会话级 connection registry，注销/禁用/改密广播撤销，或可证明上限的定期再认证；到期自动断开；流与 Job 的归属策略分开（任务是否续跑是产品决定）。验证：活动终端下改密/禁用/退出，后续输入不可达 Agent且PTY回收。

### B08 流式输入 goroutine 无取消感知 channel send【确定】

证据：httpapi/terminal.go:173–213 所有 destination <- 为裸发送；父循环因 outputDone/context return 后无人消费，缓冲满时 goroutine 永久阻塞；agentsocket/terminal_service.go:141–147 receiveTerminalMessages 同样裸发送。

独立实施：select { send / ctx.Done }，为 reader 建立独立 cancel 与完成等待；关闭策略保证 stream Recv/Write 可中断。验证：让读取端 burst 2+帧且另一端同时退出，检查 goroutine 回到基线；快断线、session close、超时三条路径。

### B09 Compose 部署缺少并发串行化，超时回滚 context 已失效【确定】

证据：compose/manager.go:70–129 全流程无项目锁/基线hash；108 备份名仅秒级且 os.WriteFile 可覆盖；115 的部署失败后119直接复用 ctx 再 Deploy。ctx 超时时恢复文件可能成功，但重建旧容器命令立即失败。HTTP compose.go:112 错误统一成“已尝试恢复”，无法供UI展示详细 rollback state。

独立实施：按规范项目ID/目标路径加锁，修改前 compare base hash；唯一不可覆盖备份；独立有界 rollback context；分别返回配置恢复、容器恢复、健康检查状态。验证：两个并发部署、同秒备份、deploy超时、rollback失败、人工外部修改。还需检查部署后健康而非只以 docker compose up -d 退出0算成功。

### B10 Agent 异常退出后残留 socket 阻止自动重启【确定条件路径】

证据：agentsocket/server.go:229–239 发现 socket 已存在直接 AGENT_SOCKET_PATH_OCCUPIED；正常 cleanup 才移除。systemd ncp-agent.service:18 RuntimeDirectoryPreserve=yes +20 Restart=on-failure。SIGKILL/崩溃不跑 defer，保留目录中 stale socket 将使下一次启动反复失败。

独立实施：保留目录inode的前提下，安全判定 stale socket（所有者/类型校验、尝试连接/进程锁）后清除；禁止随意删除任意占用文件/活跃实例。验证：临时隔离socket +子进程kill，再启动；已有活跃socket必须拒绝；非socket及symlink不可误删。

## 其他安全/数据质量缺口（P1，范围可独立）

- 登录无速率限制，默认密码最少6字符；bootstrap 在首次创建前对任何可到达者开放：auth/service.go:65–67、121–156，httpapi/handler.go:265–267。应限制尝试、提供一次性安装证明/本机初始化，不能把LAN当信任认证。当前部署 HTTP 8760→80，无TLS且 secure-cookie 默认false（deploy/console/docker-compose.yml，cmd/ncp-server/main.go:129）。实际是否有外置TLS代理未知，不能断言真实环境明文；提交的默认部署确实没有。
- NewHandler(Auth:nil) 直接放行受保护路由：httpapi/auth.go:123–126。生产 main 始终 Open auth，因此不是当前生产绕过；作为可复用构造器应 fail-closed，仅显式测试模式能免认证。
- PostgreSQL DSN 明确 sslmode=disable，MySQL未配置TLS：database/manager.go:477–482、498–504。对于跨主机数据库应增加显式TLS策略/CA/服务器名，不要隐式降级；localhost场景可单独声明。
- 数据库入站 Values/Keys 是 map[string]any，JSON decoder 未 UseNumber（httpapi/database.go:163–174），RPC 再json.Unmarshal到map（agentsocket/database_client.go:99–107）。如果API客户端提交超过2^53的JSON数字而非字符串，精度已丢失；已有输出字符串保护不解决此输入。应支持带类型值或拒绝不安全数字；验证bigint/decimal输入输出以及主键update/delete一致。
- 原始SQL returnsRows 只检查首词：database/manager.go:948–958。带注释SELECT会走Exec丢结果，INSERT/UPDATE RETURNING走Exec亦不返回行；它不是权限限制（root SQL本来允许写），应按驱动结果元数据或明确执行模式处理。SQL只限制结果行数，不限制总字节，超大单个blob仍可占用大量内存并超过gRPC默认消息限制（234–279）；可加总字节预算和blob预览/下载。
- Server 同一SQLite有auth/controlstore/credentialstore三个sql.DB。controlstore busy_timeout=5000，credentials也有；auth仅foreign_keys，未设busy_timeout（auth/service.go:492–526）。繁忙任务/指标写入时登录/会话操作可能遇到锁失败；需要统一connection pragma、迁移runner、备份/恢复策略。迁移已用IF NOT EXISTS/列存在检查，不应声称“不可重入”；不足是多连接策略与迁移版本/失败恢复。
- Compose 原文/normalized会返回敏感值，并原文持久化草稿/版本：compose/manager.go:165–169、186–190，httpapi/compose.go:119–121，controlstore/store.go:162–177。这与AGENTS“不得返回/记录密码Token”存在语义冲突，编辑器又需要原文。应明确“授权配置编辑”的敏感数据策略，默认脱敏浏览、显式敏感编辑、存储加密/受限、日志绝不含内容，而非盲目把字段抹掉损坏配置。路径校验只做词法前缀、未解析symlink（manager.go:224–249）；若声称限卷路径则可绕过，但root用户本来有完全权限，不宜当作独立提权漏洞。

## 真实功能缺口与下一阶段模块

1. 通用任务中心：目前镜像pull、Compose部署有Job，其他可能较长的容器创建/删除/项目lifecycle仍走同步HTTP。容器action有45秒 timeout（docker_handlers.go:48），镜像删除/创建最长10分钟跟随HTTP（docker_images.go:119、153附近）。应该迁移到同一Job engine，带operation id、幂等、用户、目标资源、可取消性、错误码、结果链接、真实状态复核。
2. 独立监控采集器：当前持久化指标由GET summary/SSE驱动（handler.go:386–393、474–480、575–598），无浏览器就无连续历史；多标签页重复采集。把采集时钟放server生命周期，用有界环/DB保留策略，SSE只订阅缓存的真实采集。页面的“历史监控”才不会依赖打开页面。
3. Docker实时事件：实际控制台systemEvents是周期全量poll（handler.go:472–515）。Docker Events只在internal/docker/poc.go:209和moby_gateway.go:157出现，没有生产事件总线/断线重连对账链。当前poll不应描述成event cache已实现；可先共享poll snapshot减少多客户端负载，随后增加Engine事件+断线全量reconcile。
4. 强类型RPC：*_rpc.go以structpb.Struct为参返（docker_control_rpc.go:22–32、91–108），新增.proto/生成器/兼容版本矩阵，消除JSON往返、错误码漂移和大小隐含上限。可分数据库/Job终端控制等纵切迁移，不需大爆炸重构。
5. 审计与诊断：现有middleware记录HTTP actor/method/path/status/requestId（middleware.go:40–80），但Job无actor、目标结果审计关联；后台失败被吞。增加结构化操作审计（不含SQL正文/配置密钥/终端内容）、job关联requestId、可靠性指标、可导出脱敏诊断包。root终端的HISTFILE显式持久化（host_linux.go:81–84），应避免误以为没有敏感历史，给用户清晰策略。
6. 备份/恢复与健康：SQLite、vault key、Compose修订/备份、站点图标是多份相互关联状态，需要一致性备份与恢复验证。没有依据把它们称为已有端到端灾备功能。控制面升级/降级协议、Agent能力协商与数据库迁移版本也应属于此模块。

## 推荐并行工作包及验收

- P0-A 安全资源响应：仅site_icons安全header/格式处理，浏览器恶意fixture验证，改动小可立即交付。
- P0-B HTTP认证边界：CSRF/Origin、bootstrap保护、登录限流、Auth:nil fail-closed；纯httptest加浏览器同站不同port验证。与A独立。
- P0-C Agent身份边界：专用组/peer校验/部署身份；需隔离Linux验证，不在真实NAS无批准变更。与A/B独立。
- P0-D Job不可变快照：独立小修复，race验证；先于更大的Job engine迁移。
- P1-A Job engine：有序持久化、状态机、取消、shutdown、分页/保留、异步动作迁移；fake agent/store故障注入。
- P1-B 流生命周期：channel取消、会话撤销联动、PTY进程资源收尾、SSE写失败/心跳/写deadline；Agent/HTTP共同验证。Job取消和terminal撤销不要复用同一个语义。
- P1-C Compose安全变更：项目锁、base hash、唯一备份、独立rollback budget、健康核验与部分失败状态。
- P1-D Agent重启恢复：stale socket安全清理；独立服务生命周期实验。
- P1-E 连续监控与实时快照：统一collector/scheduler+缓存fanout；测试零客户端、多个客户端、断开重连、7天保留。
- P1-F DB值与连接策略：精确类型输入、SQL返回模式、总字节预算、TLS、SQLite统一pragma；独立临时DB和fake driver可先覆盖。

后续按仓库最小验证要求执行 Go 编译、Vue类型检查/生产构建、Compose配置验证；本次只读审计未执行这些检查。并发与安全缺陷推荐追加定向race/httptest/浏览器fixture，它们是建议验收，而非声称仓库必须引入大规模TDD。真实NAS的部署健康与权限组核验只在另获明确授权后进行。


## 2026-10-13 后续认证回退复核

原始nil Auth构造器风险仍是误用边界，但生产入口在auth.Open失败时直接退出，未发现生产故障降级为匿名访问；普通认证暂时失败返回503而非登出，stream不支持provider时也拒绝。既有nil测试/嵌入兼容不在本轮默认修改范围。已用fake/recorder确认独立的bootstrap部分成功恢复缺口，见[认证恢复切片](NCP-2026-10-13-http-origin.md)。

## 2026-10-13 登录尝试与计算成本边界（A 已实施，B 仍为方案）

**代码事实。** `internal/auth/service.go` 的 `Bootstrap` 在 `WHERE NOT EXISTS` 插入检查前执行 bcrypt；因此已初始化的实例仍可被匿名 bootstrap 请求触发哈希计算。`Login` 对存在且启用的账号执行 bcrypt，计算位于 `lifecycleMu` 外；`SetMaxOpenConns(1)` 只约束数据库连接，不限制并行哈希。HTTP 两个入口均无尝试预算；已有 Origin 校验和 8 KiB 请求体限制不能替代它。未知账号/禁用账号较早失败，目前也不是恒定时间验证。

### A：已初始化 bootstrap 跳过无意义 bcrypt（已实施）

- `Service.Bootstrap` 保留 context → normalizeUsername → validatePassword 的既有顺序；在生成 hash 前调用现有 `Initialized(ctx)`。查询失败直接返回原错误，已初始化立即返回既有 `AUTH_ALREADY_INITIALIZED`。不能把预检放在输入校验之前，否则会改变现有输入错误优先级。
- 保留最终原子 `INSERT INTO users … SELECT … WHERE NOT EXISTS (SELECT 1 FROM users)` 和 RowsAffected 判定，不把它替换为普通 INSERT，也不误称现有代码使用显式事务。预检只是省 CPU，不能承担首次创建互斥；同时通过预检的请求仍可能各做一次 hash，最终只能成功创建一个账号。
- 公共 `Bootstrap(ctx, username, password)` 签名、Options、HTTP/OpenAPI/UI 无需改变。建议用私有 `bootstrap(ctx, username, password, generateHash func([]byte, int) ([]byte, error))` 实现主体，公共方法传入 `bcrypt.GenerateFromPassword`；同包测试传入计数/阻塞 fake。不要增加可由 HTTP/配置关闭哈希的开关。也可采用等价私有依赖注入，但不扩大公开配置面。
- 文件清单：运行代码仅 `internal/auth/service.go`，新增定向测试 `internal/auth/bootstrap_cost_test.go`；本审计文档记录验收。已有 HTTP 409、UI 初始化恢复契约与常规真实 bcrypt 测试继续回归，无需为了该优化修改它们。
- 临时 SQLite + 假凭据测试：已存在账号时计数 hash 为 0 且返回同码；无效用户名/密码在已初始化状态下仍保持原先输入错误；Initialized 查询错误不执行 hash/写入；空库正常请求 hash 一次并创建用户。并发测试用 fake hash 入口 barrier 让两个请求都完成预检，再放行；断言最终一个成功、一个 `AUTH_ALREADY_INITIALIZED`、users 恰好一行，证明保留的原子谓词继续工作。fake hash 故障/取消不插入用户，已有真实 bcrypt 登录用例确认公共方法仍传入真实实现。
- 此优化只消除已初始化 bootstrap 的已知浪费，不限制首次初始化并发或 Login 尝试，不能宣称已具备防暴力破解/成本限流。

### B：HTTP 共用预算（仅方案，实施另评审）

**最小切片。** 在 `httpapi` 中为同一个 handler 的 login/bootstrap 共用一个无队列准入器，生产入口仅创建一个 handler。建议默认突发 10 次、每 5 秒补充 1 次、最多同时执行 2 个凭据请求；一次 bootstrap 的创建账号和随后 Login 共占一份预算/一个许可。此数值仅是待评审的初始候选，不是目标 NAS 的实测容量，A 不会启用它。实施前须统计现有同一 handler 测试中的凭据请求总数/并发模式，说明按既定 fake clock 推进预算或显式测试依赖注入的改法；不得为通过测试静默取消生产限流，也不能未评审便改变正常 Root 登录频率。仅源代码实现与假数据验证，不新增环境变量、部署参数或代理配置；不修改密码策略、凭据、数据库结构、bootstrap 安装证明或账户锁定策略。

- 顺序保留现有 Origin、Auth 不可用、Content-Type、体积与 JSON 解析错误；成功解析后、首次调用 `Authenticator.Bootstrap/Login` 前原子地检查预算和并发容量。限流拒绝必须发生在任何账号/会话/最后登录时间变更之前，不写/清 Cookie。已取消请求不准入。拒绝不扣预算、不移动补充时间到未来，避免持续失败把等待时间无限延长。
- 许可跨整个 bootstrap → Login 调用链持有，不在创建账号后重新准入，也不将内部 Login 算为第二次外部尝试。`defer` 释放所有成功/错误/取消返回路径；不启动后台凭据 goroutine，不提前释放尚在 bcrypt 中的请求，不在准入锁内运行后端。bcrypt 本身不可取消，不以 HTTP 超时宣称已经终止计算。
- 使用固定大小状态（互斥锁、预算、时间、并发计数），不保存 username、密码、token、IP 或任意来访者 map，不启动每请求 timer/清理协程。默认使用保留单调时钟信息的 `time.Now()`，以 `Sub` 计算间隔，不先转 UTC/Unix；独立注入 fake clock。负间隔不补充、不倒退时间锚点，大间隔只补到突发上限；并发数检查与扣预算必须在同一临界区。
- 返回 HTTP 429、稳定码 `AUTH_RATE_LIMITED` 和统一中文重试提示，携带整数秒 `Retry-After`（预算不足向上取整、最少 1 秒；仅并发饱和可建议 1 秒，但不承诺届时成功）。沿用标准错误 envelope/requestId；不区分账号是否存在，不包含凭据或预算分组标识。同步更新 `api/openapi/openapi.yaml` 的两个入口及契约测试。
- 不读取 `Forwarded`、`X-Forwarded-For` 或 `X-Real-IP` 决定配额。现有 nginx 设置这些头，但 Server 无 trusted-proxy 身份/地址规则；默认请求的 `RemoteAddr` 是 nginx，而非终端用户。直接引入“按客户端 IP”既可能信任伪造头，也可能误把全部用户归为代理地址。全局固定预算无需用户先提供网络配置；将来确需逐客户端公平性时另行明确代理拓扑与可信跳数/地址范围。

**兼容与明确边界。** 普通成功响应、401/409、现有 400/413/415 和 nil Auth 语义不变；预算耗尽时合法凭据也可收到 429，这是明确新增行为。预算按 handler 隔离、进程重启重置，不是分布式限流；源地址轮换不会增加同一 handler 的配额，但持续攻击仍可能抢占正常登录机会。它限制匿名凭据尝试及 CPU 峰值，不保证登陆公平、不解决首次安装接管、不消除账号时序差异，也不限制 HTTP 连接/慢请求/解析/日志开销。直接调用 auth.Service、已认证的创建用户/改密不在该 HTTP 切片内。不要新增按账号失败锁定；部署防护、TLS、安装证明和多实例策略单列后续工作。

**前端。** 现有 `requestJson`/`NcpApiError` 可传递 429 错误码及提示；store 的 bootstrap 只对指定不确定结果执行状态复核，`AUTH_RATE_LIMITED` 不应进入复核或自动重放凭据。保留 setup/anonymous 状态，显示等待提示并允许用户稍后手动提交；本轮无需倒计时、自动重试、持久化凭据或通用 transport 改造。用 fake fetch 覆盖 login/bootstrap 的 429，以及之后手动成功提交；如果后续改为在创建账号之后限流，必须重新评审部分成功恢复契约，不能套用本设计。

**纯测试验收（无监听器/真实凭据）。**

1. 准入器单元测试：突发边界、精确补充时刻、向上取整 Retry-After、倒退/长时间时钟、拒绝不延长等待；大量不同用户名/转发头不产生键状态。并发 fake/channel 阻塞后端，确认在途上限、即时拒绝而非等待、各种退出释放许可；定向 `-race`。
2. `httptest.NewRecorder` + fake Authenticator：两路共享预算，未知/已知用户名和伪造代理头均不绕过；429 时 Bootstrap/Login 调用均为零、无 Cookie/账号/会话变更。已获准 bootstrap 必须完成两个后端步骤，不因预算正好耗尽出现新部分成功；原有部分成功错误仍按既有恢复语义返回。
3. 回归 Origin/不可用/MIME/超大/非法 JSON 优先级，确认这些拒绝不消耗凭据准入预算；`GET auth/status`、logout 和已认证 API 不受此预算影响。失败与成功登录均计入外部尝试，避免正确密码请求绕过成本上限。
4. OpenAPI 429/header 契约、Vue fake-fetch/store/view 测试；实施后按仓库要求做 Go 编译、类型检查、生产构建与静态 Compose 检查。不运行 NAS、生产端点、用户电脑、浏览器、服务监听器或真实部署。

B 仍为源代码审查后的待实施方案；未启用预算、未修改部署配置或凭据、未运行 NAS 性能基准，不能据此声称已具备防暴力破解能力。A 的实施与验证见下段。

**A 最终验证（2026-10-13 23:18 UTC）**：公开 Bootstrap 固定传入真实 bcrypt，私有 helper 仅方便同包注入计数/barrier；已初始化请求零 hash，查询错误零 hash/零写入，并发两个空库预检后仍由原子 SQL 只创建一个 Root。主任务基于102345a精确两源码文件快照通过新增组 race 三轮、完整 auth 包回归、全部命令构建及 auth vet（归档使用 -buildvcs=false）。独立复审无阻断，TestBootstrap 常规回归和新组 race 十轮通过。所有凭据均为临时测试数据，未访问 NAS 或修改实际账号。此片只消除已初始化 bootstrap 的浪费，不限制 Login 或首次初始化并发；远端 CI 尚待验证。
