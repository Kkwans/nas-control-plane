# ADR-0006：P0 journald PoC 只读、分页与可取消流边界

- 状态：已接受
- 日期：2026-07-19
- 决策范围：P0-05 journald PoC

## 背景

系统日志需要保留 journald 的结构化索引和 Cursor 能力，不能复制全部主机日志到 SQLite。与此同时，日志内容可能包含 Token、Cookie、口令或认证头；实时读取若不能在浏览器离开时及时取消，会遗留无意义的上游 `journalctl --follow` 进程。

## 决策

1. 仅由 `internal/journal` 组装固定的 `journalctl` 参数数组；不接受 Shell、命令字符串、路径或任意可执行文件。
2. 历史读取使用 JSON 输出、`limit + 1` 探测和 `--after-cursor` 分页；单次上限为 200 条。
3. `unit`、`identifier`、Cursor、时间范围和分页大小均在执行前校验。日志消息在离开模块前对 `password`、`token`、`cookie`、`Authorization` 和 Bearer 凭据片段脱敏。
4. NAS 的 systemd 252 不接受 RFC 3339 `T...Z` 作为 `journalctl --since/--until` 参数，因此统一使用微秒级 `YYYY-MM-DD HH:MM:SS.ffffff UTC` 格式。
5. Follow 使用受 Context 控制的 stdout 流；取消 Context 会关闭上游读取器，并以稳定错误码结束流。
6. `ncp-agent journal-poc` 只写入三条固定、无敏感信息的临时测试标记。它不安装服务、不持久化二进制、不接受外部日志内容或任意命令。

## 后果

- P0 已验证 journald 的查询、Cursor、unit/时间筛选、Follow 与取消收敛；这不是完整日志中心或对外 HTTP API。
- 普通 SSH 用户只能读取其可见的 journald 范围；完整系统日志仍依赖后续受控 root Agent、鉴权和审计链路，不能据此宣称已有完整主机日志权限。
- 测试标记会受 journald 的既有保留策略管理，不能逐条删除；测试使用的临时 ARM64 二进制和 NAS `/tmp` 目录在验证后必须清理。
