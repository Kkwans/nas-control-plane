# P0-05 journald PoC

## 目标

验证 NCP 在不复制全部系统日志的前提下，能通过受控 `journalctl` 调用完成 Spec 要求的查询、Cursor 分页、Follow、单元筛选和时间筛选，并在调用方停止查看后取消上游日志流。

## 范围与安全边界

- 仅执行固定的 `journalctl` 与 P0 固定标记 `logger` 参数数组；不提供通用命令执行能力。
- 历史查询限定为最多 200 条；unit、identifier 与 Cursor 使用受限字符集，时间范围必须合法。
- `journalctl` 的空结果退出码 1 归一化为空页；其他退出状态仍返回稳定失败码。
- JSON 记录只输出 Cursor、时间、unit、identifier、优先级和脱敏消息，不返回原始环境变量或命令行。
- Follow 只通过 Context 生命周期控制；取消后关闭读取器并等待终止状态，避免遗留 `journalctl --follow`。
- `ncp-agent journal-poc` 只写入 `NCP_P0_JOURNAL_QUERY_ONE`、`NCP_P0_JOURNAL_QUERY_TWO` 和 `NCP_P0_JOURNAL_FOLLOW` 三种固定测试消息；标记不包含用户输入或敏感信息。

## 验收标准

1. identifier 查询可返回结构化记录，且消息脱敏规则生效。
2. `limit + 1` 能产生 Cursor，`--after-cursor` 可得到后续记录。
3. unit 与微秒级 UTC 时间范围可共同筛选记录。
4. Follow 收到新标记后，取消 Context 会以 `JOURNAL_FOLLOW_CANCELED` 收敛。
5. 不安全筛选值在启动 `journalctl` 前返回 `JOURNAL_QUERY_INVALID`。
6. PoC 不安装 systemd 单元、不部署 Agent、不改变现有服务或容器状态。

## 验证与回滚

- 本地验证：`go test ./...`、`go vet ./...`，以及 Linux ARM64 交叉构建。
- NAS 实测：2026-07-19 在 DH4300 Plus 执行临时 ARM64 `ncp-agent journal-poc`；强制分配短生命周期 SSH TTY 后，返回 `query`、`cursorPagination`、`unitFilter`、`timeFilter`、`follow` 与 `followCanceled` 均为 `true`。
- NAS systemd 版本为 252；实测确认 RFC 3339 `T...Z` 时间格式不可解析，微秒级 UTC 空格格式可用。
- 临时目录 `/tmp/ncp-p0-journal-20260719` 与临时 ARM64 二进制均已删除。测试 journald 标记不能逐条删除，会按 NAS 现有保留策略自然过期。
- 普通 SSH 用户的可见范围不等同于 root Agent 的完整系统日志权限；P0-03 的 root systemd/Socket 实机验证仍待具备非交互提权条件后执行。

## 可追溯性

- ADR：[ADR-0006](../adr/0006-p0-journald-poc-boundary.md)
- 原始 Spec：6.6 日志中心、6.6 日志读取策略、15 Phase 0 / P0-05
