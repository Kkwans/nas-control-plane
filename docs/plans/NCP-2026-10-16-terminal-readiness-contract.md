# 终端启动确认与固定错误分类

## 背景与边界

CI `38828361955`（基线 `96133bf`）的 `TestTerminalWebSocketBridgesUnixAgentAndHostPTY` 已完成首轮 PTY、resize 与 Ctrl+C，第二连接收到 `TERMINAL_INITIALIZATION_FAILED` 而非 `started`。现有 helper 在 HTTP 101 后立即返回，但服务端在升级之后才向 Agent 启动会话；101 不能证明会话就绪。关闭中的会话仍占配额，忙错误与现象高度吻合，但现有日志未保留原始底层错误，不能认定此次失败已证实为配额忙。

另一个可确定的问题是 gRPC 状态被包装后，`status.Convert/FromError` 的 Message 可变成外层 Error 文本。旧分类器对任意 `TERMINAL_` 前缀放行、对外层文本做子串匹配，既可能误判错误码，也可能把附带内容暴露到浏览器。

## 实现

- `terminalErrorCode` 使用 `errors.As` 找到原始 `GRPCStatus`，仅取其 Message，经已有精确旧 POC 映射后，匹配固定白名单。
- 保留现有已知码，覆盖 Agent 当前发出的固定协议错误码，加入 `TERMINAL_SESSION_LIMIT_REACHED` 和固定中文提示。未知状态、普通文本、带秘密后缀、nil 状态均回退；fallback 本身同样必须是已知固定码。
- 新增测试专用、可注入 dial/read/close/pause 的启动 helper。仅首帧为合法 text JSON `started` 且 sessionId 非空、无错误码时返回成功连接。
- 只有 `error` 帧精确匹配 `TERMINAL_SESSION_LIMIT_REACHED` 才关闭该连接、等待 25ms 并重试；其余 dial/read/帧/JSON/错误码/close 故障立即失败。成功连接不关闭，首帧不会再读一次。非忙错误仅在错误码精确命中规范白名单时将固定码写入测试失败诊断，不拼接原始 Message、未知码或帧内容。
- 集成测试复用原 15 秒 context，不延长 deadline、不放宽配额、不跳过测试、不增加生产自动重连。

## 验证与验收

纯 fake 覆盖直接及一至三层 wrapped gRPC、全部白名单和 legacy 映射、unknown/nil-status/秘密后缀、伪外层错误码；fake proxy 验证 open/send/recv 忙错误与 error-frame write 失败时的固定输出和连接清理。启动 helper fake 覆盖 busy→busy→started、成功连接保留、失败即时停止、取消、过期 deadline 和原 context 透传。

本地已通过：

- `go test ./internal/httpapi -run '^(TestTerminalErrorCodeExactStatusContract|TestTerminalProxyBusyErrorContract|TestTerminalReadinessBusyThenStarted|TestTerminalReadinessRejectsWithoutRetry|TestTerminalReadinessCancellation|TestTerminalReadinessExpiredDeadline)$' -count=1`
- `go test ./internal/httpapi -run '^$'`（编译含 Linux 集成测试，不执行）
- 同一精确测试选择器加 `-race -count=10`（最终修改后再次通过）
- `go build ./internal/httpapi`
- `go vet ./internal/httpapi`
- `git diff --check`

本地没有运行真实 PTY、Unix socket、监听器、Docker 或 NAS。真实集成结果须由后续获授权的 CI 验证；不能将纯 fake 通过视为真实资源回收已通过。

根独立复跑六组新增纯fake race三轮及既有TestTerminalProxy纯测试race三轮均通过；未运行terminal_test.go的真实WebSocket监听器测试，也未运行Linux PTY集成。最终安全诊断仅输出已知固定非busy码，原始message/payload不进入新诊断。
