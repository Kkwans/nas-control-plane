# 终端快捷键与能力说明

本文档是 NCP 主机终端和 Docker 容器终端的快捷键、粘贴行为与能力回报说明。README 只保留入口，具体实现以当前会话返回的 `started` 能力字段为准。

## 粘贴

| 快捷键或操作 | 行为 |
| --- | --- |
| `Ctrl+V`（Windows/Linux） | 由浏览器 `paste` 事件读取剪贴板并写入当前 PTY |
| `⌘V`（macOS） | 由浏览器 `paste` 事件读取剪贴板并写入当前 PTY |
| `Ctrl+Shift+V` / `⌘Shift+V` | 兼容快捷键，同样走浏览器 `paste` 事件 |
| 浏览器右键粘贴 | 与上述快捷键使用同一条粘贴链路 |
| `Ctrl+Q` | 发送原 `Ctrl+V` 的 readline `literal-next` 控制字符 |

单行粘贴会直接写入 PTY。包含换行的内容会先显示确认条：

- 同时支持 `bracketedPaste` 和 `multilinePaste` 时，使用 bracketed paste 边界交给当前 Shell 处理。
- 能力未报告或不支持时，明确提示并按行发送，每行以 `Enter` 提交。
- 取消确认不会向 PTY 写入任何粘贴内容。

## 常用终端快捷键

这些按键由当前 Shell、readline 或 ble.sh 解释，浏览器不会重新定义其语义：

| 快捷键 | 功能 |
| --- | --- |
| `Enter` | 执行当前命令；多行粘贴确认后的降级发送会用它提交每一行 |
| `Ctrl+M` | 发送 carriage return；在支持多行编辑的 Shell 中用于插入或提交换行 |
| `Ctrl+J` | 发送 line feed；具体行为由当前 Shell/readline 决定 |
| `Ctrl+C` | 中断正在运行的命令，或取消当前输入 |
| `Ctrl+L` | 请求 Shell 清空当前屏幕 |
| `Tab` | 命令、路径和参数补全（仅在 Shell 提供补全时可用） |
| `↑` / `↓` | 浏览历史命令 |
| `Ctrl+R` | 反向搜索历史命令 |
| `Ctrl+A` / `Home` | 移到当前命令开头 |
| `Ctrl+E` / `End` | 移到当前命令末尾 |
| `Ctrl+U` | 删除光标前的整段输入 |
| `Ctrl+W` | 删除光标前的一个单词 |
| `Ctrl+D` | 删除光标下字符；空输入时结束 Shell |
| `PageUp` / `PageDown` | 在 xterm 滚动缓冲中向上或向下翻页；滚动条本身保持隐藏 |

## `started` 能力字段

终端握手完成后，服务端发送 `started` 控制帧。`capabilities` 可能因旧版本 Agent 或目标环境而缺省；缺省表示“未报告”，不代表支持。

| 字段 | 含义 |
| --- | --- |
| `shell` | 实际启动的 Shell，例如 `bash` 或 `sh` |
| `enhancement` | 实际增强层，例如 `ble.sh`、`readline`、`native` 或 `unsupported` |
| `reason` | 降级或能力选择的原因，不包含用户命令和敏感环境变量 |
| `resize` | 是否支持窗口尺寸同步 |
| `readline` | 是否确认使用 readline 兼容编辑能力 |
| `bracketedPaste` | 是否支持 bracketed paste |
| `multilinePaste` | 是否支持多行粘贴能力 |
| `ansiColors` | 是否支持 ANSI 颜色输出 |

主机终端会按实际探测结果报告 Bash、readline 和 ble.sh。容器终端先探测可用的 Bash/readline，失败时降级到 `sh`；容器没有安装主机的 `ble.sh` 时不会伪称具备该能力。

## 主机与容器能力矩阵

下表是能力含义，不是对每台机器的静态承诺；连接后以 `started.capabilities`、`enhancement` 和 `reason` 为准。`readline` 代表当前 Shell 已确认具备行编辑、历史和基础补全；输入高亮只在 ble.sh 实际加载成功时标记支持。

| 目标与探测结果 | Shell / enhancement | 补全与历史编辑 | 输入高亮 | bracketed / 多行粘贴 | 降级说明 |
| --- | --- | --- | --- | --- | --- |
| 主机 Bash + ble.sh 加载成功 | `bash` / `blesh` | 支持 | 支持 | 按能力字段确认 | 探测或初始化失败时回退 `native` |
| 主机 Bash，无 ble.sh | `bash` / `native` | 仅在 `readline=true` 时支持 | 不支持 | 仅在两个粘贴字段都为 `true` 时安全交给 Shell，否则按行发送 | `reason` 说明 ble.sh 缺失或不可用 |
| 主机无 Bash | `sh` / `native` | 不支持 | 不支持 | 不支持，确认后按行发送 | 回退 `/bin/sh` |
| 容器 Bash + readline/bracketed paste 探测通过 | `bash` / `readline` | 支持 | 不支持 | 支持 | 不加载主机 ble.sh |
| 容器 Bash 探测未确认 readline 或 bracketed paste | `bash` / `native`，或 `readline` 且粘贴字段为 `false` | 按 `readline` 字段显示 | 不支持 | 不支持时确认后按行发送 | `reason` 明确记录探测或配置降级 |
| 容器仅提供 `sh` | `sh` / `native` | 不支持 | 不支持 | 不支持，确认后按行发送 | 不启动 Bash 或 ble.sh |

能力字段的 `false` 是服务端确认不支持；旧 Agent 完全省略字段时，界面显示“未报告”，按不安全粘贴路径处理，不把缺省当成支持。

## 终端显示和会话边界

- xterm 的滚动缓冲仍可用键盘、触摸板或触摸屏滚动，但滚动条轨道和滑块隐藏。
- “清空内容”只清除浏览器当前会话的可见内容和滚动缓冲，不会断开 PTY、删除 Shell 历史，也不会执行远程命令。
- 终端窗口尺寸变化会通过 `resize` 消息同步；服务端不支持时，界面显示“不支持”并停止发送尺寸更新。
- 主机目标使用 Root Agent 的宿主机 PTY；容器目标使用指定运行中容器的 Shell。两者共用显示和粘贴交互，但能力徽章按目标实际结果显示。

## WebSocket 控制帧

浏览器和 Server 之间的终端 WebSocket 使用二进制帧传输输入/输出，JSON 控制帧只负责会话控制：`started`、`error`、`closed` 和 `resize`。用户输入不会被写入控制帧，也不会在能力字段中回显。
