# 历史日志查询原始输出预算

## 问题与范围

`journalctl --lines` 限制记录数量，不限制字节数量。此前 `OSRunner.Output` 使用 `Cmd.Output` 先收集完整 stdout，Reader 的 1 MiB 单行扫描上限无法保护收集阶段。

本次仅修改历史查询收集、Reader 防御检查和纯内存测试；保持公开 Runner / Follow / API 数据契约不变，不涉及真实 journalctl、宿主机、监听器或部署。

## 方案

- 每次历史查询开始时分配固定容量 2 MiB 原始 stdout 缓冲，预算包含 JSON、换行、空白和 cursor 标记。
- 正好 2 MiB 可以通过字节预算，仍需通过现有逐行解析校验。任何一次写入超过剩余空间，整次查询失败；拒绝该次写入，锁存溢出，后续写入持续失败，不返回截断数据、部分 Page 或 NextCursor。
- 私有函数参数注入执行器，无全局可变测试钩子。生产执行器设置 stdout 收集器并调用 `Cmd.Run`；发生溢出立即取消私有子 Context，等待执行器返回后再返回查询结果。调用者 Context 不被内部溢出取消。
- 优先级为调用者取消、预算溢出、原有退出码 1 正常化、其他命令结果。避免将被取消的命令或溢出加退出码 1 误判为空页。
- Reader 在解析前检查调用者取消及替代 Runner 返回的长度；替代 Runner 自身分配不受此处保护。
- 溢出沿用脱敏错误码 `JOURNAL_QUERY_FAILED`，调用者取消继续为 `JOURNAL_QUERY_CANCELED`。Agent RPC 已将 provider 错误映射为 `AGENT_JOURNAL_UNAVAILABLE`，无需新增线协议错误码。错误中不包含原始日志内容。

## 资源边界

2 MiB 是每次历史查询的原始 stdout 保留预算，不是全局并发内存预算，也不是序列化后 RPC/HTTP 响应的字节预算。解析、字符串、标准库 I/O 缓冲和响应编码有额外开销。现有 1 MiB 单行上限、分页和脱敏继续生效。Follow 的实现与缓冲不变。

## 验证

仅使用进程内假执行器和 Runner：精确边界、边界加一、超大单块、多块、溢出后重复写入；溢出加 nil / 退出码 1 / 其他退出码；父子取消隔离、调用者取消优先、预取消及已过期 deadline；并行调用隔离、等待执行器结束；预算内非法响应保持原解析错误；普通退出码 1；Reader 无部分结果、解析前拒绝、分页及脱敏。

- `go test ./internal/journal -count=1`：通过。
- `go test -race ./internal/journal -count=1`：通过。
- `go vet ./internal/journal`：通过。
- `go build ./cmd/...`：通过（仅编译，不启动服务）。
- `git diff --check`：通过。
