# NCP SQLite 连接生命周期与锁竞争修复建议

基线：2026-10-13，branch dot/ncp-modernization，HEAD f06daee；初始审计只读，基于后端审计定位后实施。未运行服务、socket、PTY、业务子进程、NAS 或真实用户数据库。复现仅使用独立 Go 程序与 os.MkdirTemp SQLite；编译依赖为本地缓存、GOPROXY=off。未修改仓库、部署或安全配置。

## 可以立即实施的最小切片

修复三个自有连接池的“连接替换后丢失原有 PRAGMA”，不把这次描述为解决所有锁竞争：

- auth：每个新物理连接保持 foreign_keys=ON；不新增 busy_timeout。
- controlstore 与自有 SQLiteCredentialStore：每个新物理连接保持现有 busy_timeout=5000；不新增 foreign_keys、WAL、同步策略或事务模式。
- 原始 DSN 原封不动交给同一个已注册 SQLite driver，不通过拼接查询参数、url.Parse 或 file: 转换修改路径语义。
- 保留原有 pool 上限、空闲连接策略、迁移顺序、迁移内容、错误码和资源归属。
- 外部注入 NewSQLiteCredentialStore(*sql.DB) 不属可重新配置的自有连接池；保留 Migrate 的现有 PRAGMA 行为，不声称能替外部池保证后续连接。三个 migrate 中原有 PRAGMA 可以先保留，避免改变该公开适配器行为。

这是低耦合的一个内部 helper、三个 Open 调用点及临时 SQLite 测试。若进一步压缩，先做 controlstore/自有 credentialstore 也成立，但 auth 外键随取消消失值得同一切片处理。跨进程迁移协调、备份恢复、锁竞争重试独立后续处理。

## 当前证据

- cmd/ncp-server/main.go:161,166,175 将同一 databasePath 分别传给 auth、controlstore、credentialstore。
- auth/service.go:97–101 直接 sql.Open，MaxOpenConns(1)；605–633 仅在迁移期间执行 foreign_keys=ON。
- auth/streams.go:14–15 当前 authDatabaseTimeout 为 **2 秒**，不是 200 毫秒。调用方的更短 deadline 仍然有效。
- auth/service.go:218–247、383–421、435–465、494–522 的事务受生命周期锁保护，并依赖 commit 后再撤销流；不可只重试某条 UPDATE 或先撤销后写库。
- controlstore/store.go:225–236 与 685–687：busy_timeout=5000 只在迁移执行。
- database/credentials_store.go:82–98 与 145–157：同样只在 Migrate 设置；NewSQLiteCredentialStore 的外部 *sql.DB 和 owned 生命周期必须保留。
- modernc.org/sqlite v1.54.0/conn.go:937–976：ResetSession/IsValid 将被中断的文件连接标为不可用；内存连接有特殊保留行为。这会在真实 context 取消后自然触发新连接，而不是仅测试强制回收。
- driver tx.go:35–57：Commit 使用 context.Background()，失败时驱动会尝试 rollback。不能假设 Tx.Commit 随 BeginTx context 中断。

## 临时 SQLite 已完成复现

复现实验在独立临时目录使用与项目相同的依赖版本执行。所有数据库均为临时新建、结束时删除；未读取用户数据库。

最近一次实际输出：

```
initial foreign_keys=1 busy_timeout=5000
replacement foreign_keys=0 busy_timeout=0
write busy=0 deadline=200ms elapsed=116.536µs err=database is locked (5) (SQLITE_BUSY)
write busy=5000 deadline=200ms elapsed=5.011003624s err=context deadline exceeded
deferred read-to-write busy=1000 elapsed=31.759µs err=database is locked (5) (SQLITE_BUSY)
commit busy=1000 deadline=200ms elapsed=1.00289174s err=database is locked (5) (SQLITE_BUSY) ctx=context deadline exceeded
interrupt err=context deadline exceeded subsequent fk=0 busy=0
DSN initial fk=1 busy=25
DSN replacement fk=1 busy=25
```

replacement 用 SetMaxIdleConns(0) 强制回收后恢复 1；interrupt 用 10ms context 的纯递归 SQL 触发真实取消。DSN 实验仅证明 driver 支持每连接初始化，不是推荐的路径改写方案。

结论：当前 auth 零 busy timeout 可立即锁失败；增加 5000 会使 200ms 请求阻塞约 5 秒，且阻塞时 auth lifecycleMu 可能一并拖住其他认证/撤销/关闭操作。deferred 事务先读再写的升级冲突还可能完全不调用 busy handler，因此加 timeout 也不能解决全部失败。不得把 busy timeout 直接设为 2 秒就声称已遵守 context；更短调用方 deadline 和 commit 都不成立。

## 供实施参考的 helper 核心代码（尚未应用）

包名可取 internal/sqliteconn。以下用已注册 driver，不创建全局 hook、不替换全局 driver，也不丢掉其已注册扩展。PRAGMA 参数仅由三个内部调用点提供固定常量。

```go
package sqliteconn

import (
    "context"
    "database/sql"
    "database/sql/driver"
    "errors"
)

func Open(dsn string, pragmas ...string) (*sql.DB, error) {
    // sql.Open 对当前 modernc Driver 不建立物理连接。
    probe, err := sql.Open("sqlite", dsn)
    if err != nil { return nil, err }
    base := probe.Driver()
    if err := probe.Close(); err != nil { return nil, err }
    return sql.OpenDB(&connector{
        base: base,
        dsn: dsn,
        pragmas: append([]string(nil), pragmas...),
    }), nil
}

type connector struct {
    base driver.Driver
    dsn string
    pragmas []string
}

func (c *connector) Driver() driver.Driver { return c.base }

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
    if err := ctx.Err(); err != nil { return nil, err }
    conn, err := c.base.Open(c.dsn)
    if err != nil { return nil, err }
    fail := func(err error) (driver.Conn, error) {
        _ = conn.Close()
        return nil, err
    }
    if err := ctx.Err(); err != nil { return fail(err) }
    exec, ok := conn.(driver.ExecerContext)
    if !ok { return fail(errors.New("sqlite connection does not support context execution")) }
    for _, pragma := range c.pragmas {
        if _, err := exec.ExecContext(ctx, pragma, nil); err != nil {
            return fail(err)
        }
    }
    if err := ctx.Err(); err != nil { return fail(err) }
    return conn, nil
}
```

三个生产调用点分别由 sql.Open("sqlite", path) 改为：

```
sqliteconn.Open(databasePath, "PRAGMA foreign_keys = ON")
sqliteconn.Open(databasePath, "PRAGMA busy_timeout = 5000")
sqliteconn.Open(path, "PRAGMA busy_timeout = 5000")
```

helper 需要保证 sqlite driver 已注册：可在 helper 中统一 blank import modernc.org/sqlite，并清理调用方仅为注册的重复 import；或者保留调用方现有注册方式。不能返回包装过的 driver.Conn 丢失 Validator/SessionResetter 等接口；这里返回原始 conn，保留所有接口。不能使用全局 sqlite.RegisterConnectionHook，因为其他数据库管理功能也用 sqlite driver，超出这三个元数据库池的范围。

说明：这个参考实现保留当前 driver.Open 的不可取消物理 open 行为，检查 open 前后 context，不宣称让底层 open 可中断。这里执行的固定 PRAGMA 是连接属性设置，没有主动创建数据库事务。正式实现应先独立编译、用 fake driver 检查失败关闭、取消和传参，再验证实际三个池。

## 必须覆盖的最小验证

1. fake driver 记录每次收到的 DSN，包含相对路径、空格、问号、井号、百分号、反斜线、file: URI、:memory: 和 file::memory:；要求逐字节保持输入。不擅自“修复”原有 modernc 对普通字符串问号的解析语义。用 fake 可以避免特殊路径在真实文件系统被误解释。
2. 每个 owned store 在临时文件上检查初始 PRAGMA、强制回收后的 PRAGMA、真实递归 SQL 取消后的 PRAGMA。auth 的 foreign_keys 必须继续为 1；默认 busy 保持 0；另外两池 busy 必须继续 5000。
3. 临时 users/sessions 数据验证 auth 连接替换后拒绝孤儿 session、删除用户仍级联；保留登录事务、撤销成功/失败和 admission 的现有测试。无真实凭据；使用生成的虚拟 fixture。
4. helper fake 验证配置失败、open 后取消、配置后取消时新连接被关闭；成功连接不被提前关闭；不改变调用方 sql.DB.Close 的归属。
5. 临时 memory 用例避免强制销毁唯一内存连接；验证初始行为、取消后数据仍可用，重复 Open/Close 与 URI 内存使用不回归。不要把空内存数据库自然消失误报为迁移持久化问题。
6. 迁移重复运行、已有 schema 打开不丢记录、三个 store 顺序打开同一临时文件；注入外部 *sql.DB 的 credential adapter 的 Migrate/Close 行为不变。
7. 同一进程中普通 sql.Open("sqlite", ...) 不受 helper 影响；现有 database manager/discovery 连接不获得额外 PRAGMA。
8. 编译与有界 auth/controlstore/credential 测试、需要时 race；不执行会启动 sockets、外部进程、PTY 或真实服务的广泛套件。

## 锁竞争后续切片边界

独立设计 context-aware 等待，宜保持 SQLite 自身 busy wait 为零或经过明确延迟预算评审的小值，使用 select(timer, ctx.Done) 等待且有总预算。对事务重試必须 rollback 后从权威读取开始整个事务重来；不能自动重放已成功 commit、不能重复随机 token/外部副作用、不能在未确定提交失败时声称撤销成功。BEGIN IMMEDIATE 能把部分写锁竞争前移，但会改变事务语义，单独验证而非顺手全局加 _txlock。

只对明确 SQLITE_BUSY 类结果采取有界策略，避免对磁盘满、只读、损坏、constraint 和 SQLITE_LOCKED 全部盲目重试。包括提交时 reader 锁冲突、deadline 到期、持锁期间取消、重试完成后的 session/stream 一致性；保留当前先成功持久化后 revoke 的顺序。迁移竞态、跨进程 writer 和 WAL/备份一致性仍是不同工作，不在最小 PRAGMA 耐久性补丁范围。

## 错误与清理边界补充

- helper 只沿用原驱动错误，不给错误或日志拼接 DSN、连接字符串、路径参数或 PRAGMA 用户值；不新增任何含 DSN 的日志。若现有 driver 错误包含输入，则沿用已有外层稳定错误码/脱敏边界，不扩展暴露。
- 初始化失败时 helper 负责关闭刚建立的 driver.Conn；Open 后迁移失败时原 store.Open 负责关闭自有 sql.DB，维持现有资源生命周期。外部注入的 credential DB 不转为 owned，也不由适配器关闭。
- 用临时 sql.Open 对象获取 DB.Driver 后立即 Close，无实际连接创建，既保留已注册 driver 的函数/选项，又避免独立 new(sqlite.Driver) 丢失全局配置。单元测试应记录委托到同一实例。

## 实施验收状态

2026-10-13 15:15 UTC：连接初始化helper与三个自有池接入已实现，生产diff保留原PRAGMA、池配置、迁移与所有权。初始临时SQLite与fake测试通过，但独立race复验发现10ms取消用例有时在SQL实际执行前超时，不能证明物理连接确实被中断。正在修正确定性执行证据；未将该失败测试跳过，也尚未提交该模块。

### 15:26 UTC 验证修正与冻结

中断测试改为测试专用SQLite标量函数在真实执行时触发取消，要求执行标记、context.Canceled以及文件连接身份变化全部成立；5秒只作失败保护，不用增加sleep或重试掩盖race。生产代码未因该测试修正改变。

实现者及独立复核均通过四包精确race×20、完整auth/controlstore、有限credential回归、vet与build。三个集成调用只替换Open入口，auth仍不增加busy_timeout，外部注入credential数据库所有权不变。完整远端CI待运行；这不是所有数据库锁竞争或超时行为的修复。
