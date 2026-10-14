# 目录浏览读取权限提示：Linux 窄修复

日期：2026-10-14。基线：18e4bb8。本切片仅调整 Root Agent 目录条目的可读提示，不增加路径白名单、不禁止符号链接或特殊文件，也不改变实际 Docker 挂载权限。

## 问题与方案

创建容器的 HostPath 选择器调用 `GET /api/v1/files/entries`，经 Agent `ListPath` 到 `filesystem.Browser.List`。前端用 `type` 决定目录能否进入，用 `readable` 决定条目能否选择。旧实现逐项 `os.Open`，浏览含 FIFO 的目录可能等待写入端，打开设备也可能阻塞或触发设备行为；请求取消和 HTTP 的 5 秒超时不能中断该次 `open`。

生产 Linux 改用 `unix.Faccessat(AT_FDCWD, path, R_OK, AT_EACCESS)`：只查询当前有效身份的权限，保持跟随符号链接。普通文件、目录、链接、其他条目使用同一检查器；检查失败仍保留条目，只将 `readable` 设为 false。非 Linux 通过独立构建标签保留原有打开探测行为，本次不承诺其特殊文件安全性改进。

`readable` 是选择器的瞬时权限提示，不能作为后续读取或挂载的授权凭据。特殊文件（包括 socket）可能从旧打开失败时的 false 变为权限检查成功时的 true；这是去除打开副作用后的提示语义，不表示端点已被打开、可立即读取或实际操作获准。实际资源操作仍由原有执行路径校验。

## 保留的边界

- x/sys v0.47.0 在 Linux 优先使用 `faccessat2`；遇 ENOSYS/EPERM 会以 `Fstatat` 和 UID/GID/权限位回退。回退不完整重现 ACL、LSM 与 capability 等内核权限判断，因此提示不能保证后续 `open` 成功。
- 符号链接目标和权限可在检查后变化，TOCTOU 仍然存在。不能通过先 `Stat` 再 `Open` 保证特殊文件不被打开；本修复直接移除 Linux 条目的内容打开。
- `ReadDir`、权限检查等文件系统元数据调用仍可能受网络文件系统或异常存储阻塞。本次只在调用间及排序后检查 context，不承诺内核调用可被取消，也不以遗留后台 goroutine 伪装硬超时。
- `limit` 只限制返回条目及逐项检查次数。现有全目录读取、全量排序、偏移分页不变；未新增目录扫描/内存硬预算，动态目录也没有快照一致性保证。

## 隔离验证范围

纯 fake 验证所有条目类型均走权限检查、路径与分页范围、缺失/权限错误不删除条目、取消后停止继续检查；普通临时文件、目录和指向普通临时目标/缺失目标的符号链接验证 Linux helper。未创建或打开 FIFO、设备或 socket，未启动 Docker、PTY、listener、浏览器或 NAS 服务。

已通过：
- `go test -race ./internal/filesystem ./api/openapi -count=1`。
- `go vet ./internal/filesystem ./api/openapi` 与 `git diff --check`。
- Linux amd64/arm64、`CGO_ENABLED=0` 的 `ncp-server` 与 `ncp-agent` 编译。
- Windows amd64 / Darwin arm64 的 `internal/filesystem` 测试二进制交叉编译，仅编译、未执行。

这些结果只验证隔离环境中的代码与 mock 行为，不表示真实 NAS、旧内核回退、ACL/LSM 或异常网络存储已验证。
