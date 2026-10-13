# M08-C1 容器终端的有界中断与关闭

日期：2026-10-13。基线：7dc9d2159b098ebf0197207d4d643b31a13d48ac。状态：代码与独立隔离审查完成；分支完整CI待验收。

## 目标与范围

活动流撤销已经到达Server与Agent流；本片继续解决已取得Docker exec attachment之后，容器输入、调整尺寸和关闭同步阻塞的问题。保留Root终端能力、Shell选择、Session与RPC对外契约。

不包含ExecAttach返回前的SDK握手、Manager.Start全局持锁重构、宿主机PTY与任意子孙进程终止。不得为了结束一个终端而停止整个容器、杀PID 1、遍历杀进程，或限制Root用户的既有管理能力。

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

本片不承诺总开启时间有界，不修复ExecAttach前同步握手及Manager.Start持锁，不承诺宿主机PTY的非阻塞状态、直接child的等待或所有后代回收。容器shell可忽略控制字节或留下脱离任务，后续须先定义所有权和观测策略。两秒关闭加100ms最后通知还存在调度开销，不宣称RPC精确在2.000秒内结束。
