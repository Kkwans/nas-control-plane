# M08-C1 容器终端的有界中断与关闭

日期：2026-10-13。基线：7dc9d2159b098ebf0197207d4d643b31a13d48ac。状态：容器片6cb1688已推送并通过完整CI；主机H1追加片见文末，独立记录验证状态。

## 目标与范围

活动流撤销已经到达Server与Agent流；本片继续解决已取得Docker exec attachment之后，容器输入、调整尺寸和关闭同步阻塞的问题。保留Root终端能力、Shell选择、Session与RPC对外契约。

不包含ExecAttach返回前的SDK握手、Manager.Open全局持锁重构、宿主机PTY与任意子孙进程终止。不得为了结束一个终端而停止整个容器、杀PID 1、遍历杀进程，或限制Root用户的既有管理能力。

## 已确认问题

- 既有取消和关闭回调会同步写Ctrl-C/exit；关闭计时器在写入之后才开始，写卡住时预算无效。
- 输入及Resize可占住Agent消息主循环，不能依靠主循环再次观察取消来解除它们。
- Moby attachment提供缓冲Reader和net.Conn，必须保留Reader中已读入数据，同时直接控制同一连接。SDK没有ExecKill接口；关闭传输并不证明exec退出。
- Close时提前释放Manager配额，会允许关闭资源仍在占用时不断建立新会话。

## 设计契约

1. Attachment保留由同一net.Conn驱动的缓冲Reader。收紧任意Writer/回调契约，不保留不可中断的隐式兼容路径。失败出口关闭已取得资源。
2. 生命周期取消只执行幂等context取消与Conn.Close，不写控制字节、不执行远端请求。Read前后检查取消；使用AfterFunc时停止或等待已启动的回调，避免将stop=false误认为已结束。
3. 输入操作串行化且等待可取消，取得操作权后重验active状态。正常Write使用独立两秒绝对write deadline；部分成功不可重放，设置或清除deadline失败时终止传输。
4. Resize继承session生命周期并有两秒预算；Close/abort取消其context。依赖gateway遵守HTTP context契约，不用遗留阻塞goroutine伪装超时成功。
5. Close从第一步创建唯一两秒清理预算，标记closing后拒绝新输入。已有I/O占用或已取消时直接abort；活跃且可立即取得操作权时才尽力写Ctrl-C/exit，各阶段共用同一截止时间。
6. 无条件关闭连接后，在剩余预算内Inspect等待exec主进程退出。超时、404或Docker不可达均是退出未确认；仅Running=false可确认主进程退出。错误保存并支持重复Close，不将传输结束解释为进程树全部结束。
7. Agent统一一次cleanup；显式关闭后的最后通知额外最多等待100ms，然后返回RPC。初始started帧也由唯一输出sender负责，主循环不会被首帧发送挡住取消。RPC返回才释放的Send/Recv不在返回前强制join；本地Reader及输入操作在剩余关闭预算内等待完成，预算耗尽返回独立的本地清理未确认错误，不伪称全部owner已结束。取消回调须停止或等待完成。
8. Manager从可访问map摘除session后保留closing reservation，直到本地Close返回才释放。未确认远端退出须保留准确警告，不永久占住配额或掩盖残留风险。

## 验收矩阵

- 父取消打断Read、堵塞Write和Resize；取消后不再发送缓冲输出或新控制字节。
- Write/Close竞争、已占用操作锁、反复Close返回同一结果；deadline设置/清除失败、部分写失败无重放。
- 整个Close共享预算；Inspect超时/404/网络错误不判已退出；transport失败不能被覆盖。
- 关闭中配额不提前释放；关闭后本地配额恢复。
- Agent初始/最后输出阻塞时仍能响应取消或预算并返回；测试在返回后释放模拟transport并验证泵退出，不能用假context契约证明不存在泄漏。已中断但本地Read/Resize收尾仍延迟时，明确返回本地清理未确认，与exec退出未确认分开。
- 纯fake/barrier测试及race在隔离环境执行；完整Go与构建由分支CI补充。真实Docker/NAS、真实PTY行为不在本次验证范围。

## 保留缺口

本片不承诺总开启时间有界，不修复ExecAttach前同步握手及Manager.Open持锁，不承诺宿主机PTY的非阻塞状态、直接child的等待或所有后代回收。容器shell可忽略控制字节或留下脱离任务，后续须先定义所有权和观测策略。两秒关闭加100ms最后通知还存在调度开销，不宣称RPC精确在2.000秒内结束。

## M08-H1：主机 PTY 的可取消 I/O 与直属进程回收

基线：208bc58262445180cbea87ea2ae91b2b70e08575。状态：96070cd已推送且完整CI通过；纯fake竞态、跨平台编译及隔离Linux真实PTY/直属child用例通过，不宣称NAS实机验收。

### 已确认问题与范围

已有 Resize 经 pty.Setsize 调用 File.Fd，可能把 master 切回阻塞模式；File.Read/Write 也可能在 Go poller 内等待，外层 EAGAIN 检查不足以保证取消。旧 Close 不等待有效 Wait 结果，重复调用还可能丢失首次错误。

本片只处理已成功启动的主机 PTY、启动成功后的失败出口、直属 child 的唯一回收责任。保留 Root、Shell/rc/环境、增强能力、初始窗口大小与 RPC 契约。不终止任意后代、进程组或其他终端，不调整系统权限。

### 实现契约

1. master 通过 SyscallConn.Control 设置非阻塞；Read/Write 的 RawConn 回调只做一次系统调用并返回 true，让应用层控制 EAGAIN/EINTR 等待。Resize 通过受保护 fd 执行 TIOCSWINSZ，禁止运行路径再调用 File.Fd 或 pty.Setsize。
2. 读操作与写入/调整尺寸分别使用可取消 gate。写入两秒总预算覆盖排队、部分写入与重试，只推进已写后缀，不重放输入。Resize 的排队同样有预算。
3. 父取消、显式关闭拒绝新输入并中断本地等待；自然退出须保留可立即读取的尾部输出，直到 EOF/EAGAIN，不等待仍持有 slave 的后代。
4. 每个直接 child 只有一个 Cmd.Wait owner。非零退出和信号终止的有效 ProcessState 也属于已回收，不能只以 WaitErr==nil 判定。
5. Close 从入口共享两秒清理预算，保存唯一结果；区分本地 I/O/关闭未确认与直属进程退出未确认。迟到 Wait 继续由原 owner 接收，不把既已返回的超时改成成功。
6. 已取得 PTY 后的初始化失败和取消走同一清理路径；Agent 日志只记录安全的分类标志，不记录终端输入或底层敏感错误文本。

### 验收与真实边界

- 本地只执行纯 fake/barrier 的生命周期、取消、并发 Close、部分写入、预算和错误分类测试及 race；真实 PTY/直属进程用例仅在既有隔离 Linux CI 执行。
- 内核用例检查 Resize 前后 O_NONBLOCK、空读取消、写背压、直属 child 的零/非零/信号退出回收；保留既有交互 PTY 回归。
- 两秒预算约束应用层等待，依赖正常 Linux 非阻塞 syscall/ioctl 返回；不宣称可抢占任意内核 D-state、驱动卡死或精确墙钟硬截止。
- nil Close 仅证明所拥有资源与直接 child 的回收结果，不代表所有后代已消失或 Root 命令副作用撤销。
- Manager.Open 全局锁、PTY 创建之前的 locale/能力探测、容器 attach 之前的握手仍是独立缺口；NAS 实机未验证。

## M08-H2：开启预留与短锁发布

基线：96070cd00476e26bbd9ac8b69f236033b67f7e7e。状态：037f91f已推送且完整CI通过，纯fake竞态与独立审查完成。

### 问题与设计

Manager.Open原先在全局锁内调用Starter.Start及Metadata。一个慢启动会阻塞已有会话的查询/输入/关闭，甚至使达到上限的新请求不能及时拒绝。

- 短锁内保留opening名额，配额始终计入opening + live + closing，保留默认上限1及全部配置。
- 同步调用Start及Metadata，但移出全局锁；不增加超时后遗弃的启动goroutine，不以context取消作为已释放资源的证据。
- 发布时短锁复核取消并原子将opening转为live；取消或错误但返回了session时，原子转为closing，在锁外Close，结束后才释放名额。
- 非nil session与错误同时返回也须有唯一清理者。保留主错误码及errors.Is清理因果；Agent初始Open失败出口安全记录未确认清理，不让取消映射nil吞掉诊断。
- 成功发布是归属转移点；紧随发布发生的取消由既有session/Agent清理路径处理，不重复关闭。

### 验收与保留范围

使用纯fake/barrier证明慢Start/Metadata不挡其他manager操作、容量无间隙、晚到成功只关闭一次、错误出口名额恢复、并发唯一ID、发布前后取消以及安全日志。真实Starter在配置容量大于1时须支持并发并返回独立session；不使用原有非并发安全fake重复同一对象。

不新增不存在的CloseAll或每用户配额，不修改host/container传输、协议或依赖。卡住的Starter仍占其名额，H2不保证单次Open总时长有界；locale、PTY创建和SDK attach握手保留为独立问题。

H2审查补充：裸主错误与带码的清理错误组合时，显式固定原主错误码（含空码），防止次级取消/目标错误改变原RPC失败分类，同时保留errors.Is因果。生产starter的每次会话资源独立；Moby首协商失败后重试存在静态观察到的潜在内部version并发边界，尚未动态复现，不将此片描述为SDK所有路径并发保证，默认容量1保持不变。
