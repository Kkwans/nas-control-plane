# NCP 接手以来的累计改动

检查点：2026-10-14 21:14 UTC，分支 `dot/ncp-modernization`。比较范围：[接手基线 5b81557](https://github.com/Kkwans/nas-control-plane/commit/5b81557068a9a44cae3b06ac6207438a399e8ef7) → [已推送代码 a72ba8b](https://github.com/Kkwans/nas-control-plane/commit/a72ba8bd29fdb0d6239ba46251f98ce4f14bd808)。

接手时，Docker、Compose、数据库、终端、站点等模块已经存在。本轮保留原有架构，增加日常管理能力，重点修复操作结果不可信、迟到请求覆盖新页面、断连后重复写入和资源回收问题。

## 各模块实际变化

### 1. 监控：历史连续性与状态可信度

**新增**服务端独立采集，关闭网页仍能采集历史，多标签共享实时快照；历史图按真实时间展示缺口，支持有界温度曲线。增加 CPU/内存持续高占用及恢复提示，默认关闭，没有外部通知。**修复**旧数据被当作健康、局部采集失败被掩盖的问题。

依据：[独立采集](https://github.com/Kkwans/nas-control-plane/commit/e776c1707a5bdd83bd53cf59913ab7ef28c48a61)、[历史曲线](https://github.com/Kkwans/nas-control-plane/commit/52a7d8541b06ae697c3387739eecf84c62bc0c60)、[资源提示](https://github.com/Kkwans/nas-control-plane/commit/d5108ea2b1359a389a0e1f0a41ee7d69db0ac9ad)。

### 2. Docker 与 Compose：操作后能核对真实状态

**新增**共享 Docker 实时采集、事件失效通知和断线全量对账。**修复**容器创建结果不明时可重复提交、镜像删除误报、下载观察中断无恢复入口等问题。Compose 保存、校验、确认绑定同一内容版本；部署检查磁盘基线，避免覆盖外部改动，超时后仍尝试有界回滚，并分别呈现配置与运行恢复结果。

依据：[事件对账](https://github.com/Kkwans/nas-control-plane/commit/f2580b728794a54db2e80fe1b9e5a581b90a1f7f)、[创建防重放](https://github.com/Kkwans/nas-control-plane/commit/22d08db296e09b3e4c0d6bcb8f1f93f85933d656)、[Compose 基线](https://github.com/Kkwans/nas-control-plane/commit/47c078c8e63245ef6add4da5577070545a14d65a)。

### 3. 数据库：精度、编辑安全与 SQL 反馈

**新增**自动/返回行/影响行数三种结果模式、结果字节预算；普通 CTE 写入计数及部分 SQLite 单条写入 RETURNING 自动展示。SQL 每次请求只选一条执行路径。**修复**大整数主键与小数精度、主键识别、重复列别名、分页丢草稿和编辑器快捷键/只读状态。新增、修改、删除或 SQL 结果未确认时保留风险提示，阻止无意识重复操作；表结构改变不会静默重解释旧草稿。

依据：[精度](https://github.com/Kkwans/nas-control-plane/commit/9bcd424b39d08cf3df702556535feda036b85998)、[结果模式](https://github.com/Kkwans/nas-control-plane/commit/76a461c376c702e7b30d5dd18627ccf943a93235)、[未知结果](https://github.com/Kkwans/nas-control-plane/commit/42f87a8afa6f6dc33efadf1fc8d66ca1b393653f)、[SQLite RETURNING](https://github.com/Kkwans/nas-control-plane/commit/43d1742995ed75e27a4e25cf2e0c281cc96f2dae)。

### 4. 终端：独立标签与可靠关闭

**新增**最多四个独立页面标签，隔离连接、输入焦点与输出；Agent 容量可配置为 1–4，默认仍为 1。**修复**阻塞输入、主机 PTY 关闭、直属进程回收、慢启动及容器握手取消；限制输入输出积压，关闭活动连接需确认，未收尾连接继续占用容量。

依据：[标签](https://github.com/Kkwans/nas-control-plane/commit/53962e4541d0812266afd2fe6ed566f82ff11a13)、[主机回收](https://github.com/Kkwans/nas-control-plane/commit/96070cd00476e26bbd9ac8b69f236033b67f7e7e)、[容器握手](https://github.com/Kkwans/nas-control-plane/commit/d146b110e6f21c738ad03764830a9944d53e7cf1)。

### 5. 任务与日志：跨页面追踪、有限导出

**新增**最近任务抽屉，可查看镜像拉取和 Compose 部署的状态、详情与可取消操作；日志可导出当前页快照，最多 200 条、2 MiB。**修复**任务快照竞争、持久化乱序及取消提前报完成。任务抽屉尚非完整审计历史，日志导出范围也仅为当前页。

依据：[任务生命周期](https://github.com/Kkwans/nas-control-plane/commit/168855fc1f72c07e9365cf7e925794bebb0cefab)、[最近任务](https://github.com/Kkwans/nas-control-plane/commit/f06daee07e9f25d7fbdf41378edbad4d89c31d74)、[日志导出](https://github.com/Kkwans/nas-control-plane/commit/ff92b16ec422d9008312dd4cfac35ca39899e5ed)。

### 6. 认证与站点：补齐安全及部分成功边界

**修复**退出、改密或禁用后活动终端/实时流仍可继续的问题；增加写请求来源、JSON 类型/体积校验及登录/初始化共享请求预算。上传 SVG 采用隔离响应。站点资料和图标分别确认结果，图标失败保留已创建站点，迟到上传不能恢复已删除资料或覆盖并发编辑。

依据：[会话撤销](https://github.com/Kkwans/nas-control-plane/commit/7dc9d2159b098ebf0197207d4d643b31a13d48ac)、[认证预算](https://github.com/Kkwans/nas-control-plane/commit/85219166815a3fe1b7870e5285fc77b53e5291cb)、[SVG 隔离](https://github.com/Kkwans/nas-control-plane/commit/32b2a47e45a3655d15054e817f1f03f366994e58)、[图标归属](https://github.com/Kkwans/nas-control-plane/commit/ea6d0922fcbc8b5fb8fb591fcb984be8f59efc66)。

### 7. 系统、账号、设置与界面

**修复**DNS 预览/确认、账号弹窗、设置自动保存的请求归属；读取失败、写入失败和结果未知分别呈现，旧请求不能覆盖新目标。移动导航补齐焦点隔离，改善窄屏和大字号下的日期、长名称、工具栏与风险说明；修复多处对比度、键盘操作和弹窗语义，并实时响应减少动态效果偏好。

依据：[DNS 确认](https://github.com/Kkwans/nas-control-plane/commit/744d39d782e0684a9d96380cbfad2ff21afa8974)、[账号写入](https://github.com/Kkwans/nas-control-plane/commit/9baa9f5f0148b1ce1f0c0918706deeec70c2c113)、[设置归属](https://github.com/Kkwans/nas-control-plane/commit/b88214f62a7512c0bf376a5202f07ba0a53b03ea)、[移动导航](https://github.com/Kkwans/nas-control-plane/commit/00c5f8ff466bd796cc47a4c516493e81d2364170)、[SQL 编辑器](https://github.com/Kkwans/nas-control-plane/commit/f2cce02d7be1760d666e99689b5ed8d9d81abb5c)。

## 上次报告之后：已推送的新增与修复

以下为 43d1742 之后的增量，已包含在上述累计范围内。

### 数据库连接与证书校验 TLS

- **新增完整代码链路**：MySQL/PostgreSQL 可显式设置 verify-full，选择 Agent 系统根证书或自定义 PEM CA，并填写独立服务器验证名。策略贯通界面、严格 HTTP 校验、加密凭据保存、Agent RPC 与连接器；旧 Agent 不支持时明确失败，不移除 TLS 自动重试。
- **保护保存与重试边界**：凭据版本快照与条件写入阻止迟到连接覆盖新凭据；冲突保留草稿并要求明确重试。已保存严格策略不会因省略字段静默降级，取消界面编辑也不等于关闭已保存策略。
- **修复连接与数据归属**：区分连接诊断、凭据保存未知、已确认后目录失败；校验目录和行数据的源、表身份，保留稳定 RPC 错误码并清理远端错误详情。补齐连接诊断对比度及 TLS 键盘路径。
- **验证边界**：设置入口和请求内容不能证明服务器已应用 TLS，界面仍保留“状态未确认”；尚未进行真实 PostgreSQL/MySQL TLS 握手验收。

依据：[凭据并发保护](https://github.com/Kkwans/nas-control-plane/commit/c8a9de2864c0612a16a9e2e8d5038a3011a810e0)、[数据身份校验](https://github.com/Kkwans/nas-control-plane/commit/556216ee2c4af9a5b9a28a46e6eb67dbbf0205d3)、[TLS 后端与协议](https://github.com/Kkwans/nas-control-plane/commit/0263c2f61be4975fbcc8492238abe278ad558e32)、[TLS 表单与 HTTP](https://github.com/Kkwans/nas-control-plane/commit/23fef4aee21c5f51ea029e803558b6f08e57d638)、[诊断与键盘修复](https://github.com/Kkwans/nas-control-plane/commit/beb86a989d66026db67741bcf9bdfcde859657a7)。

### Docker 操作回执与目录浏览

- **修复生命周期结果核验**：严格绑定动作和容器/项目目标，只接受匹配的成功回执；命令回执与读回运行状态分别呈现。结果未知时保留风险提示，只读刷新不能抹去，确认等待期间阻止重复派发。
- **修复镜像重试刷新**：重新下载完成后可靠刷新本地镜像清单，并补充权限撤销与迟到请求隔离的浏览器覆盖。
- **修复可访问性与窄屏**：操作提示归入主内容地标，告警关闭按钮不再收缩换行。浏览器覆盖未知回执、状态不符及明确确认路径；提示浮层定位在后续 883eb7d 的实际截图和几何复核中已关闭。
- **Linux 目录浏览不再逐项打开文件探测权限**，避免 FIFO、设备等特殊文件因探测产生打开副作用。可读性仍只是瞬时权限提示，不保证后续读取或挂载成功，也不代表所有文件系统调用都能取消；非 Linux 保留原行为。

依据：[镜像刷新](https://github.com/Kkwans/nas-control-plane/commit/b66ab09a6f723d601901372b48af5496e4cdf674)、[严格操作回执](https://github.com/Kkwans/nas-control-plane/commit/be18a45d64512c4169fb44a1d93cb17e990a41e1)、[浏览器动线](https://github.com/Kkwans/nas-control-plane/commit/18e4bb8146ddeb5a03b513536f0a54ea2c0192f8)、[提示与窄屏修复](https://github.com/Kkwans/nas-control-plane/commit/78df508dcd64b8529ef870d7ad252a4ba7e59929)、[Linux 权限探测](https://github.com/Kkwans/nas-control-plane/commit/2e0b7c02330235009174b270b4c8b9024b4b7c22)。

## 创建容器：已推送的精确输入修复

- [a659a8d](https://github.com/Kkwans/nas-control-plane/commit/a659a8d832d9421a76d2cfe0ab95c7a3623ad2c4)：使用镜像默认命令时，在 RPC 中省略 command，避免空参数数组被旧解码器拒绝。
- [ad56e93](https://github.com/Kkwans/nas-control-plane/commit/ad56e93303637e198f312815a351d1ef8c527257)：挂载与目录浏览保留路径名称中的空白，防止选中的目录被静默换成另一条路径；卷名维持原规范化。
- [ffba19d](https://github.com/Kkwans/nas-control-plane/commit/ffba19df5a5fd334050ad57e70adf0f5dd942567)：设备映射同样保留精确路径，拒绝原始非法路径和重复目标，保持设备权限字段原规则。
- [883eb7d](https://github.com/Kkwans/nas-control-plane/commit/883eb7d8bb79a3e86f45846a08031bc3a4185e35)：逐行启动参数保留非空行原值，确认预览使用 JSON 数组区分参数边界；全空白仍采用镜像默认命令，空行处理有明确说明。
- [cd40c15](https://github.com/Kkwans/nas-control-plane/commit/cd40c15c5c5701599edde66f5f633f966a1aa0cc)：操作等待期间关闭旧位置提示，允许重新悬停定位；浏览器回归增加按钮与提示间距、居中及三阶段几何检查。883eb7d 的实际 CI 图片显示，桌面提示与按钮间距约 12px、中心差小于 0.5px，原有明显错位不再出现。

另以 [bfe7ed5](https://github.com/Kkwans/nas-control-plane/commit/bfe7ed58bcf4bec3b166b1acca483768c1de8a6d) 保留参数预览中的连续空格，新增四项 390/1440 创建确认与精确输入浏览器用例；其 [CI 38638232519](https://github.com/Kkwans/nas-control-plane/actions/runs/38638232519) 仍在运行，不能据用例枚举声称已验收。

[a72ba8b](https://github.com/Kkwans/nas-control-plane/commit/a72ba8bd29fdb0d6239ba46251f98ce4f14bd808) 另将提交后的精确请求、201 回执与观察记录持久化到 CI 产物，并与确认前截图证据明确区分；真实运行结果仍待 CI。

本地已通过 3547 项前端测试（124 文件）、lint、类型检查、构建及包预算，相关 Go race、全树编译和 vet 通过。源码已推送；883eb7d 已获完整 CI 通过，bfe7ed5 的新增预览样式和四项浏览器用例仍待独立结果。没有访问真实设备、Docker 或 NAS。

## 验证状态与尚未完成的部分

- **883eb7d** 的 [CI 38636791987](https://github.com/Kkwans/nas-control-plane/actions/runs/38636791987) 全部适用检查通过：3546 项单元（124 文件）、417 项浏览器用例，Go/Compose 成功，发布跳过。

- 已推送 **78df508** 的 [CI 38624509376](https://github.com/Kkwans/nas-control-plane/actions/runs/38624509376) 全部适用检查通过：3521 项前端单元（121 文件）、417 项浏览器用例，以及 Go/Compose 检查；Release 步骤跳过，不能称为已发布。
- TLS 界面修复 **beb86a9** 的 [CI 38612535338](https://github.com/Kkwans/nas-control-plane/actions/runs/38612535338) 已通过：3411 项前端单元（119 文件）、413 项浏览器用例及 Go/Compose 检查。
- Docker 八张 PNG 对应的完整 axe 扫描零违规，但仍有三类 incomplete；883eb7d 的八组截图、完整 axe、352 项源码哈希及 PNG 哈希已逐项复核，三阶段几何一致，桌面提示定位与 390px 关闭按钮均符合本轮限定检查；不据此声称全面无障碍或视觉验收合格。此前 SQL 四张截图属于 f2cce02 检查点，不替代后续改动验收。
- 浏览器使用隔离 API 数据；部分集成使用临时 SQLite、文件和受控子进程。真实 NAS、Docker、PostgreSQL/MySQL TLS 握手及原生多会话尚未验收。**代码已推送不代表部署或发布；本轮没有部署或发布。**
- 下一步先核验新创建输入用例的 CI 和参数预览实际布局，再继续可隔离验证的开发。Agent 专用访问身份、更完整的跨引擎 SQL 自动识别、慢操作统一任务化、跨页面幂等回执及一致性备份恢复仍待推进。数据库 TLS 已从方案推进到完整代码链路，真实握手验收仍待完成。现有页面防重复保护不能替代服务端幂等，Compose 内容核对也不能证明应用运行健康。

## 后续验证补充（2026-10-14 22:16 UTC）

前述待验创建流程已经完成一轮失败定位、修复和复验，并非初次运行即成功：首轮四项新用例暴露删除图标无名称、高级配置说明对比不足、确认弹窗重复 banner，以及偏好读取 fixture 缺失。

- [37a77b5](https://github.com/Kkwans/nas-control-plane/commit/37a77b56960d775e23612850b2e41f41c30b8c26) 修复上述产品语义与样式，补齐准确的偏好读取响应；没有缩小完整 axe 扫描或放宽未知请求拒绝。真实 DOM 回归覆盖两个弹窗的 Tab 循环、Escape、焦点归还及层叠。
- [d0f08a6](https://github.com/Kkwans/nas-control-plane/commit/d0f08a63eba891e0ebc659a725f98578b1d008ee) 将当前位置改为具名导航并标注唯一当前页面，保留祖先链接和现有焦点逻辑。
- [CI 38641143224](https://github.com/Kkwans/nas-control-plane/actions/runs/38641143224) 全部适用检查通过：125 文件、3558 单元测试、421 浏览器用例、11 制品分片测试及 Go/Compose 检查；发布步骤跳过。
- 四张 390/1440 参数与默认命令预览 PNG、完整 axe JSON、独立提交后回执均已核验。[制品分片 3](https://github.com/Kkwans/nas-control-plane/actions/runs/38641143224/artifacts/11931007096) 中 354 个源码哈希与该提交一致，CI 工作树为空，图片和回执引用哈希匹配。确认前零写入；确认后精确一次 POST、201 响应、六秒无重放，未知请求和页面错误均为空。
- 启动参数 JSON 空白与折行可见，预览几何稳定。路径字段通过精确请求核验，不宣称路径摘要中的尾空白具有视觉辨识度。四张图完整 axe 零违规，当前位置相关 aria-prohibited-attr 不再列为未决；仍有 aria-valid-attr-value 与 color-contrast 两类 incomplete，需要人工和后续专项验证，不能称为全面无障碍验收。
- [f3efd48](https://github.com/Kkwans/nas-control-plane/commit/f3efd48990d353a6db0cf885e54d7e5ea2d58eee) 修复 SQLite BOM 前导语句的自动结果识别，隔离真实 SQLite 覆盖单次执行和返回行。原始 SQL 保持不变；其他方言不扩展 BOM 规则。

以上仍是源码、隔离 SQLite 与浏览器 fixture 证据，没有 NAS、真实 Docker 或 PostgreSQL/MySQL TLS 实机验收。接续工作包括弹窗实际浏览器键盘动线和更完整的 SQL 结果模式边界；后续未推送代码不计入上述远端完成量。


## 后续验证补充（2026-10-15 00:40 UTC；00:43 UTC 更新）

当前已推送代码为 [080447a](https://github.com/Kkwans/nas-control-plane/commit/080447a08756bb5bcb22aade276a31d46a2b369e)。上文保留各历史检查点的当时结论；本节补充后续结果，不能将早期通过记录视为最新界面的完整验收。

- **SQLite 结果模式**：[57ae940](https://github.com/Kkwans/nas-control-plane/commit/57ae940a0ba3c182a6bb201a9a705c2fb5fb6d3b) 补齐直接 `REPLACE … RETURNING` 的自动返回行；随后 [ceb427e](https://github.com/Kkwans/nas-control-plane/commit/ceb427e68ee7e6ddb86b4bf39c9134157d88bd44) 修复 SQLite `WITH … REPLACE` 无 `RETURNING` 时自动模式丢失影响行数。保留原始 SQL、单次执行与显式模式边界，不扩展到其他方言。后者本地数据库全量 race（8.979s）、全树编译和 vet 通过；[CI #343 / 38659943284](https://github.com/Kkwans/nas-control-plane/actions/runs/38659943284) 的 Go、Compose、Frontend 已全部通过，发布跳过。
- **创建确认的输入边界**：在前述 d0f08a6 当前位置语义与 37a77b5 初轮弹窗修复之后，[30b744e](https://github.com/Kkwans/nas-control-plane/commit/30b744e6ff96ebb8f47b3c66aeaa99ca56804e81) 以带引号的路径预览明确挂载/设备边界；[b190db1](https://github.com/Kkwans/nas-control-plane/commit/b190db1918d0a06f27e2ee9a4e519b479985206e) 明确主机 IP、主机端口与容器端口；[621535d](https://github.com/Kkwans/nas-control-plane/commit/621535dd2a3b6d4bb265b4a50f382ea8e7d76656) 分别标明 privileged、capAdd 与 capDrop，避免混淆权限增减方向。
- **浏览器失败与复验**：[3ca3d36](https://github.com/Kkwans/nas-control-plane/commit/3ca3d36c4f563acd803319c59ad17a5cc5d8ba24) 的端口用例曾出现四项超时；[af2c3e2](https://github.com/Kkwans/nas-control-plane/commit/af2c3e2abe65f337355013b85dd2755e8613399f) 将点击目标改为协议选择器的实际可见包装入口。[CI #342 / 38655370576](https://github.com/Kkwans/nas-control-plane/actions/runs/38655370576) 全部适用检查通过：128 文件、3569 单元测试、427 浏览器用例及 Go/Compose；发布跳过。这是修正测试目标后的复验结果，并非首轮成功。
- **已核验的证据与局限**：[制品分片 3](https://github.com/Kkwans/nas-control-plane/actions/runs/38655370576/artifacts/11936392857) 与[分片 4](https://github.com/Kkwans/nas-control-plane/actions/runs/38655370576/artifacts/11936397829) 中八组 PNG、完整 axe 与提交后回执，以及两份实际浏览器键盘 JSON，均已核验 357 项源码哈希及图片/关联证据哈希。预览与规范化请求一致，确认后一次 POST、201 响应及六秒无重放；键盘记录覆盖两个弹窗的焦点循环、Escape 与焦点归还。完整 axe 零违规，仍保留 aria-valid-attr-value、color-contrast 两类 incomplete，不代表全面无障碍合格。
- **绿色 CI 仍遗漏了实际窄屏问题**：af2c3e2 的 390px 参数/路径截图中页脚位于视口下方，通配地址/默认端口截图中页脚贴底并被裁切。此前文本几何通过和后续成功点击回执，只能证明相关检查及滚动后操作可达，不能证明截图时页脚可见。因此这些截图不构成完整视觉验收。
- **最新修复尚待实际图片复核**：[080447a](https://github.com/Kkwans/nas-control-plane/commit/080447a08756bb5bcb22aade276a31d46a2b369e) 限制确认弹窗高度，将详情设为具名、可聚焦的独立滚动区，保留标题和页脚；浏览器检查补充实际面板/页脚边界、按钮命中及 Home/End 滚动可达性。本地 128 文件、3570 单元测试、lint、类型检查、构建及 10 项 E2E 静态检查通过。00:40 UTC 时 [CI #344 / 38660310549](https://github.com/Kkwans/nas-control-plane/actions/runs/38660310549) 的 Go/Compose 已通过、Mock E2E 尚在运行；00:43 UTC 确认浏览器结果为 **426/427，未通过**。390px 参数/路径用例因滚动条占用宽度后，行内保留空白在行尾悬挂，触发 `fitsLabel` 失败。该用例的实际面板纵向边界为 16–828px，页脚按钮为 772–812px，命中检查通过；End/Home 使详情滚动位置从 37px 回到 0，标题和页脚保持不动。这些限定几何证据支持操作区可达，但不能替代完整视觉验收。当前正修复产品中的路径折行；后续仍需重新运行 CI 并核验实际图片，不宣称本轮全绿或像素问题已全部关闭。

以上仍限于源码、隔离 SQLite 和浏览器 fixture；真实 NAS、原生 Docker、PostgreSQL/MySQL TLS 握手及原生多会话尚未验收。本轮没有部署或发布。


## 窄屏确认流程复验关闭（2026-10-15 01:01 UTC）

在保留上述 080447a 失败记录的基础上，[bdd852f](https://github.com/Kkwans/nas-control-plane/commit/bdd852f4d91f55f8928d4d542fd1f6143bca1498) 将精确路径改为块布局与 `break-spaces`，使连续空白参与折行宽度计算，保留原始 JSON 字符。[CI #346 / 38661847385](https://github.com/Kkwans/nas-control-plane/actions/runs/38661847385) 全部适用检查通过：128 文件、3570 单元测试、427 浏览器用例、11 项制品分片测试及 Go/Compose。

- [制品分片 3](https://github.com/Kkwans/nas-control-plane/actions/runs/38661847385/artifacts/11939801879) 与[分片 4](https://github.com/Kkwans/nas-control-plane/actions/runs/38661847385/artifacts/11939975851) 的八张实际 PNG、八组配对捕获 JSON/提交回执及两份键盘 JSON 已复核；357 项源码哈希、图片与关联证据哈希一致。
- 八组文本与标签包含关系、三阶段稳定几何、面板/页脚边界、按钮命中和标题/详情/页脚不重叠检查均通过；保留空白的字形宽度为正（6px），路径没有靠删除空格通过检查。390px 参数/路径面板纵向为 16–828px，按钮为 772–812px，均在 844px 视口内且命中有效；End 将详情滚到 82px 并显示最后字段，Home 回到 0，标题与页脚不动。其他七组无需额外滚动，End 位置为 0。
- 每组确认后精确一次 POST、201 响应、六秒无重放；完整 axe 零违规，仍保留 aria-valid-attr-value 与 color-contrast 两类 incomplete 人工审查项。本轮限定的窄屏页脚与路径折行问题已通过实际截图和对应几何复验，不扩展为全面无障碍或真实环境验收。

资源限制与重启策略的后续改动正在实施，尚未完成，不计入本次完成范围。以上仍为隔离浏览器/SQLite 证据，没有真实 NAS、原生 Docker 或真实数据库 TLS 验收，也没有部署或发布。


## 资源与重启预览、网络复用复验（2026-10-15 01:39 UTC）

最新已推送代码为 [c2d14af](https://github.com/Kkwans/nas-control-plane/commit/c2d14afac0adb735eda8ee80edde752c6ac40ab4)。本节接续上次尚未完成的资源限制与重启策略工作，保留前述历史结论。

- **资源与重启快照**：[0eee0a1](https://github.com/Kkwans/nas-control-plane/commit/0eee0a1a4877f9755e3e4b8f8932a59c011ef0cb) 增加 CPU、内存、重启策略及重试次数的确认预览。四项新增浏览器用例在 [CI #348 / 38663732203](https://github.com/Kkwans/nas-control-plane/actions/runs/38663732203) 因选择器问题失败，原有 427 项通过；随后 c2d14af 为三个数字输入显式绑定可访问名称，并修正重启策略 combobox 的相对作用域，保留精确请求、几何及完整 axe 检查。
- **网络复用边界**：[6f12be3](https://github.com/Kkwans/nas-control-plane/commit/6f12be3af9f2b81b9c815d8701f5c3b287cc58c2) 在复用同名网络前校验有效驱动及同一匹配子网条目的显式网关；仅明确 404 不存在时创建，权限、服务或传输失败均不触发写入。16 项 fakeRoundTripper 回归、Docker 全包 race、全树构建与 vet 通过，并同步 OpenAPI；不代表真实 Docker 网络验收。
- **最新完整 CI**：[CI #350 / 38665490164](https://github.com/Kkwans/nas-control-plane/actions/runs/38665490164) 全部适用检查通过：129 文件、3585 单元测试、431 浏览器用例、11 项制品分片测试及 Go/Compose。新增四项已完成失败后的复验，不能记为首轮成功。
- **实际证据复核**：[制品分片 3](https://github.com/Kkwans/nas-control-plane/actions/runs/38665490164/artifacts/11942035542) 与[分片 4](https://github.com/Kkwans/nas-control-plane/actions/runs/38665490164/artifacts/11942180492) 中 12 张实际 PNG、12 组配对捕获/提交回执及两份键盘 JSON 均已检查；每组 358 项源码哈希与该提交一致，记录的 CI 工作树干净，归档、图片及关联证据哈希匹配。390/1440 资源行和页脚完整可见，文本边界、面板/页脚几何、按钮命中及 Home/End 固定标题页脚检查通过；键盘焦点循环、Escape 归还和取消零写入均通过。
- **预览与提交一致**：限定资源场景显示 1.5 核、402653184 字节（384 MiB）、on-failure 与 3 次重试；默认资源场景明确本次请求未设置 CPU/内存限制，on-failure 的 0 次重试明确为不设重试上限。12 组均确认前零写入，确认后精确一次创建 POST、201 响应、六秒无重放，无未知请求或页面错误。完整 axe 零违规，仍保留 aria-valid-attr-value 与 color-contrast 两类 incomplete 人工审查项，不据此宣称全面无障碍合格。

以上是源码、内存传输与浏览器 fixture 证据；真实 NAS、原生 Docker、PostgreSQL/MySQL TLS 握手及原生多会话仍未验收。本轮没有部署或发布。


## 网络与 GPU 确认详情复验（2026-10-15 02:39 UTC）

最新已推送代码为 [cf01fec](https://github.com/Kkwans/nas-control-plane/commit/cf01fec005ac1c676f3d10f93862a39d22249025)。以下接续前一检查点，保留失败、修复及复验的先后记录。

- **网络确认详情**：[3629e93](https://github.com/Kkwans/nas-control-plane/commit/3629e9328ca341ee7a13cdffb59b5fd19185cb7d) 展示网络名称、驱动、子网、网关和固定 IP，区分默认连接、已有网络及条件创建/复用。CI #352 的四项新增用例因点击隐藏开关 input 失败，原有 431 项通过；[86f5752](https://github.com/Kkwans/nas-control-plane/commit/86f5752212735d314285a7b1ef1942113cb5aa0f) 改为点击实际可见开关核心，并加强 checked 状态断言，没有强制点击或放宽检查。[CI #353 / 38668747550](https://github.com/Kkwans/nas-control-plane/actions/runs/38668747550) 全部适用检查通过：130 文件、3597 单元测试、435 浏览器用例、11 项制品分片测试及 Go/Compose；16 张实际 PNG、16 组配对回执、两份键盘 JSON 与 359 项源码哈希完成复核。
- **GPU 确认前校验**：[8cd9094](https://github.com/Kkwans/nas-control-plane/commit/8cd909422f6d93b540ac7201ebdf7ec88dbecc90) 阻止启用 GPU 时的 0、null、非整数数量及无效驱动/能力进入确认，保留 -1 表示全部、空能力默认 gpu、混合大小写提交原值及禁用时忽略残留值的语义。CI #354 通过 131 文件、3615 单元测试、435 浏览器用例及 11 项制品分片测试。
- **GPU 请求快照**：cf01fec 逐项显示自动/指定驱动、全部/指定数量及按 Agent 规则规范化的请求能力；例如自动场景的 -1 与空能力显示“全部”和默认 gpu，指定场景保留请求中的 `Nvidia_1.test-driver`、数量 2 及 `[GPU, Compute, utility_1]`，预览规范化能力为 `[gpu, compute, utility_1]`。预览取自待提交快照，保留编辑失效与精确提交边界。[CI #355 / 38670577374](https://github.com/Kkwans/nas-control-plane/actions/runs/38670577374) 全部适用检查通过：132 文件、3622 单元测试、439 浏览器用例、11 项制品分片测试及 Go/Compose。
- **最新实际证据闭环**：[制品分片 3](https://github.com/Kkwans/nas-control-plane/actions/runs/38670577374/artifacts/11944413902) 与[分片 4](https://github.com/Kkwans/nas-control-plane/actions/runs/38670577374/artifacts/11944209052) 的 20 张实际 PNG、20 组配对捕获/回执及两份键盘 JSON 均已复核；361 项源码哈希与 cf01fec 一致，记录的 CI 工作树干净，归档、图片及关联证据哈希匹配。原有 16 组预览仍通过；新增 GPU 字形均位于可见边界内，390px 指定 GPU 场景 End 滚动至 33px 后可见最后字段，页脚按钮位于 772–812px，处于 844px 视口内并通过命中检查，Home 归零且标题/页脚固定。长内容通过详情区滚动查看，不声称所有正文同时出现在截图内。
- **写入与无障碍边界**：20 组均确认前零写入，确认后精确一次创建 POST、预期 201 回执、六秒无重放，无未知请求或页面错误；两份键盘记录覆盖弹窗层叠、焦点循环、Escape 归还及六秒零写入。完整 axe 扫描没有缩小规则或排除范围，零违规，仍保留 aria-valid-attr-value 与 color-contrast 两类 incomplete 人工审查项，不代表全面无障碍验收。

以上仍是源码与隔离浏览器 fixture 证据，不证明真实 GPU 分配、原生 Docker 或 NAS 可用；真实数据库 TLS 握手及原生多会话也仍未验收。本轮没有部署或发布。


## 单镜像删除持久任务闭环（2026-10-15 03:27 UTC）

- **新增单目标 Job 链路**：[598f939](https://github.com/Kkwans/nas-control-plane/commit/598f939f1b785814df825de5b25b1213f97e7899) 增加 `POST /api/v1/docker/images/remove-job`，只接受完整规范镜像内容 ID，持久接收后返回原始 queued 202 快照。独立串行 worker 复用现有共享容量 32，仅调用一次既有批量删除 RPC 的单元素请求，保留引用预检与非强制删除；无取消/重试，重启不重放，无法确认的执行结果归为 interrupted。前端严格校验接收回执、保留同会话未确认锁，提供最近任务和镜像清单只读核对；原多选批量路径保留。相关 Go race、OpenAPI、全树构建/vet 与最初 3668 项前端单元通过。
- **保留首次失败与修复**：[CI #357 / 38673340124](https://github.com/Kkwans/nas-control-plane/actions/runs/38673340124) 的四项新增浏览器用例失败，439 项旧用例通过：残留 ElTooltip 遮挡手机操作，桌面完整 axe 发现 region 问题。[54f0cec](https://github.com/Kkwans/nas-control-plane/commit/54f0cecf83460c9ad12d72c4955e0697de9f0b91) 将提示挂入 main 地标并在删除等待期间禁用；[CI #358 / 38674968351](https://github.com/Kkwans/nas-control-plane/actions/runs/38674968351) 全部适用检查通过：134 文件、3669 单元测试、443 浏览器用例、11 项制品分片测试及 Go/Compose，没有削弱原断言。
- **实际图片与请求证据**：[制品分片 3](https://github.com/Kkwans/nas-control-plane/actions/runs/38674968351/artifacts/11946466840)、[分片 4](https://github.com/Kkwans/nas-control-plane/actions/runs/38674968351/artifacts/11946466845) 与[分片 5](https://github.com/Kkwans/nas-control-plane/actions/runs/38674968351/artifacts/11945982192) 中六张新增实际 PNG 和四份最终请求记录已复核：完整镜像 ID 可见折行，手机只读刷新/最近任务按钮无遮挡，44px 最小按钮与命中检查通过。每份记录仅一次携带 imageId 的删除任务 POST；分别核对严格 queued 202 接收、错误 reference 的 202，以及实际 `net::ERR_FAILED` 且无响应状态/正文的传输丢失。之后六秒只读观察，没有重发、同步接口降级、未知请求或额外页面错误。
- **接收、完成与当前库存分开**：接受任务不显示为镜像已删除；错配或丢失回执明确提示不要重复提交。完成任务的只读回执与最初接收状态分开呈现，仍要求刷新本地镜像以核对当前状态。四份记录的后续请求均为 GET，每份镜像列表读取共三次，任务读取在已接收场景共两次、未确认场景一次。
- **完整回归与边界**：全部 26 组捕获链、既有 20 组创建回执及两份键盘 JSON 已重新核验 364 项源码哈希、干净的 CI 工作树、归档/PNG/关联哈希与几何。既有创建图中 17 张与前轮逐图审查版本字节相同，另三张变化图重新目视检查通过；仍保持精确一次创建 POST、201、六秒无重放及键盘零写入。完整 axe 零违规；接收界面保留 aria-valid-attr-value、color-contrast incomplete，完成任务读回另有 aria-prohibited-attr incomplete，仍需人工审查，不能称全面无障碍合格。

本片完成的是源码、fake 后端与 CI 浏览器 fixture 的隔离验证；不提供跨请求幂等，不代表真实 Docker/Agent socket、NAS 或真实持久库重启集成验收。没有部署、发布或生产操作。


## 批量删除回执、项目状态与最近任务性能（2026-10-15 04:08 UTC）

最新已推送代码为 [5284ef3](https://github.com/Kkwans/nas-control-plane/commit/5284ef374d31869c43d96c25bc7681085da19344)。以下分别记录批量流程的实际制品复验与最新性能提交的完整 CI 结果；上文历史记录保持不变。

- **批量镜像删除确认与回执**：[86b566a](https://github.com/Kkwans/nas-control-plane/commit/86b566aac2c27676f5063a6d5f679bc8839281af) 在确认前预留同会话目标、冻结镜像身份，确认后重新核对页面、会话与引用状态，并联锁单项/批量操作。前端与 Agent RPC 严格核验目标集合、计数及错误语义；未知结果跨页面重挂载保留同会话锁，只读刷新不解除风险。保留同步接口、每批最多 50 项及非强制删除，**没有将批量删除改为持久 Job，也不提供跨请求幂等**。[1dae796](https://github.com/Kkwans/nas-control-plane/commit/1dae796277b095dde64ca68e3ab95677a4afc2ab) 增加 `redirect: error`，阻止 307/308 自动重放 POST，并将异常重定向回执归为未知而不重试。本地全量通过 3743 项前端单元（135 文件）；重定向切片通过 220 项定向 API 测试、lint 与类型检查，批量切片的纯 RPC 定向 race、构建和 vet 通过。
- **独立项目完成状态**：[0244ed9](https://github.com/Kkwans/nas-control-plane/commit/0244ed98037c98a35ce1a64fec6f608b899c2c87) 要求启动/重启读回 running、停止读回 stopped，才将该容器项标为成功；状态不符保留实际观察与稳定错误码，项目汇总不再误报 completed。不增加动作重试或额外读取。纯 fake 回归、生命周期定向 race 20 轮、OpenAPI、vet 和构建通过，不替代真实运行健康验收。
- **批量流程完整 CI 与实际证据**：0244ed9 的 [CI #362 / 38677740189](https://github.com/Kkwans/nas-control-plane/actions/runs/38677740189) 全部适用检查通过：135 文件、3753 单元测试、447 浏览器用例、11 项制品分片测试及 Go/Compose。[制品分片 4](https://github.com/Kkwans/nas-control-plane/actions/runs/38677740189/artifacts/11947762227) 与[分片 5](https://github.com/Kkwans/nas-control-plane/actions/runs/38677740189/artifacts/11947482402) 中六张新增实际 PNG、四份最终请求记录及关联捕获链已复核，366 项源码哈希与该提交一致，记录的 CI 工作树干净，归档、PNG 和关联哈希匹配。390/1440 混合结果明确显示一项删除、一项因引用跳过，`completed: false`；390px 外来目标及 1440px 重复目标回执均被拒绝，原两个目标保持结果未知，未显示任务已接收。完整 ID 可见折行，稳定几何、44px 最小按钮及命中检查通过。
- **写入与无障碍边界**：四份记录均保持确认时冻结的两个 ID，即使选择随后变化也不替换请求目标；各自仅一次批量 POST、200 JSON 回执，后续均为只读 GET，镜像列表共读取三次，六秒内无重放、降级写入或 Job 创建，无未知请求或页面错误。上述六张图完整 axe 零违规，仍保留 aria-valid-attr-value、color-contrast 两类 incomplete 人工审查项，不代表全面无障碍验收。
- **最近持久任务读取性能**：5284ef3 拆开混合与精确类型查询，新增含完整时间和 ID 次排序的两个索引，保留原索引、完整历史记录与既有限额。固定 modernc SQLite 的临时库验证了生产 SQL 查询计划、与旧查询的结果等价、取消及旧结构重复迁移；完整 controlstore race（58.565 秒）、vet 和全部 cmd 构建通过。十万条历史的本地暖缓存单次样本中，混合读取由 430.18ms 降至 2.31ms，指定类型由 141.71ms 降至 2.36ms。样本不包含索引创建，不代表 NAS、冷缓存或并发负载；新增索引仍有首次迁移、写入和磁盘成本，不据此声称整体启动耗时有界。
- **最新性能提交 CI 已通过**：04:08 UTC 确认 5284ef3 的 [CI #363 / 38678326726](https://github.com/Kkwans/nas-control-plane/actions/runs/38678326726) 全部适用检查通过：135 文件、3753 单元测试、447 浏览器用例、11 项制品分片测试及 Go/Compose，发布步骤跳过。此处已核验终态与日志计数；上面的实际图片及请求记录复核仍对应 #362 / 0244ed9，不宣称已独立复核 #363 的制品。

以上为源码、纯 fake/内存传输、临时 SQLite 和 CI 浏览器 fixture 证据；真实 Docker/Agent socket、NAS、真实数据库 TLS 握手及原生多会话仍未验收。没有部署、发布或生产操作。


## 只读持久任务历史交付与复验（2026-10-15 05:14 UTC）

最新已推送代码为 [56bd3d0](https://github.com/Kkwans/nas-control-plane/commit/56bd3d0e2853093b4a513dcc76a9e025729b9d77)。本节保留首次失败、证据缺失与后续复验的区别，不将最终绿色 CI 写成首轮成功。

- **H0 后端历史接口**：[cf33588](https://github.com/Kkwans/nas-control-plane/commit/cf33588cd74615bd646ebac8470d5fe9458de0e3) 新增已认证 `GET /api/v1/job-history`，从实际任务引擎的持久库读取有界摘要，以创建时间和 ID 做 keyset 分页，复用已有索引。严格校验查询、游标、文本与 1 MiB 响应预算，不修改历史、不访问 Agent，也不改变原最近任务接口。完整 controlstore race 通过（142.884 秒）；后续仅测试强化的两项 race 通过（51.582 秒），HTTP recorder 定向 race 三轮、OpenAPI、构建、vet 及独立审查通过。[CI #365 / 38681323634](https://github.com/Kkwans/nas-control-plane/actions/runs/38681323634) 全部适用检查通过：3753 单元/135 文件、447 浏览器用例、11 项制品分片测试及 Go/Compose。此提交仅交付后端，不代表当时已有历史浏览界面。
- **H1 独立只读历史视图**：[aebba45](https://github.com/Kkwans/nas-control-plane/commit/aebba450f4ec31ee85ea1bff2f3d201ee75a6bb8) 在任务抽屉增加“最近任务 / 已保存历史”，支持精确类型、页大小、较早页与回到最新页；严格解码有界 API，只保留当前页，失败保留原页与原游标，关闭/切换/换会话隔离迟到响应。最近视图拆分后保留原观察能力，历史不提供执行动作、轮询或实时流。本地 3835 单元/139 文件、lint、类型检查、构建及包预算通过；实际浏览器验收由后续 CI 完成。
- **首次失败与证据容量修复**：[CI #366 / 38681991222](https://github.com/Kkwans/nas-control-plane/actions/runs/38681991222) 为 450 通过、三项新增失败：390px/200% 字号的摘要视口比例为 0，另两项空态/错误态触发 aria-prohibited-attr。制品超过原八片上限后未上传任何分片，因此没有该次失败的截图或几何证据。[11edc08](https://github.com/Kkwans/nas-control-plane/commit/11edc0852298e30987240908f8492d64ea651ba2) 将有界分片上限增至 16，保留每片 24 MiB、元数据约束及 14 天保留期，12 项分片测试通过；没有削减证据或测试门槛。随后 #367 为 451 通过、两项相同 aria 失败，九片制品可取；移动大字号用例在没有布局修改的情况下通过。
- **语义修复与完整复验**：56bd3d0 将具名历史列表改为 section，修复空态/错误态 aria 语义，并增加三项单元测试；浏览器仅补充祖先滚动与焦点诊断，**没有实施移动布局修复**。本地 3838 单元/139 文件及 lint、类型检查、构建预算通过。[CI #368 / 38684015062](https://github.com/Kkwans/nas-control-plane/actions/runs/38684015062) 全部适用检查通过：3838 单元/139 文件、453 浏览器用例（原 447 项及六项历史用例）、12 项制品分片测试和 Go/Compose，发布跳过。
- **实际图片与请求证据**：[制品分片 6](https://github.com/Kkwans/nas-control-plane/actions/runs/38684015062/artifacts/11949269022)、[分片 7](https://github.com/Kkwans/nas-control-plane/actions/runs/38684015062/artifacts/11949189271) 与[分片 8](https://github.com/Kkwans/nas-control-plane/actions/runs/38684015062/artifacts/11949738747) 中 40 张实际历史 PNG、40 份完整 axe 捕获及六份最终请求记录已复核；375 项源码哈希对应 56bd3d0，记录的 CI 工作树干净，归档、图片与关联记录哈希一致。覆盖 390/1440、100%/200% 字号，以及翻页失败、精确类型、不支持、错误恢复、空页和重新打开。可见长文本折行、抽屉边界、44px 最小按钮及命中检查通过；长内容仍需纵向滚动，不能据截图声称全部正文同时可见。完整 axe 零违规，保留 color-contrast incomplete 人工审查项。
- **只读与请求归属**：六份请求记录全部为 GET，实际历史请求序列与预期一致；下一页失败后用同一游标重试，回最新/切类型/页大小正确重置游标，精确类型保留两端空白。没有历史动作、Agent 调用、实时流、未知请求或页面错误。键盘焦点/Escape 归还、迟到读取隔离及 60.1 秒模拟时钟下无轮询由该提交通过的源用例证明；最终请求记录没有单独序列化这些键盘断言或模拟时长，不能从 PNG 推导。
- **仍未解释的移动偶发失败**：#368 的 390px/200% 摘要在三阶段几何稳定，位于视口内并通过命中；#367 未改布局也有同样的通过记录。由于 #366 缺少制品，最初视口比例 0 的根因仍未知，两次通过不能证明该偶发问题已根治。

历史仅显示最后成功保存的状态，可能落后于执行，也不证明当前资源状态；跨页没有快照或总数保证，1 MiB 是响应预算而非进程内存上限。异常记录明确失败，历史清理、导出和执行动作不在本片范围。以上仍是临时 SQLite、fake 与 CI 浏览器 fixture 验证；真实 NAS、Docker/Agent socket、数据库 TLS 握手及原生多会话仍未验收，没有部署、发布或生产操作。


## 数据库操作边界与历史字号时序复验（2026-10-15 18:59 UTC）

最新已推送代码为 [9aa04d1](https://github.com/Kkwans/nas-control-plane/commit/9aa04d1d018ae93bca7ae0c4fe213da4687079ee)。以下接续前述历史交付，区分数据库修复、既有浏览器用例的重复失败与测试时序修复后的结果。

- **数据库共享操作预算**：[11351ce](https://github.com/Kkwans/nas-control-plane/commit/11351ce0f92a506a199701ba28f2d6435dee97d9) 为同一 Manager 的连接池拥有操作提供固定四个许可，普通与 TLS 路径共享，满额立即拒绝而不排队；连接池关闭后才幂等释放许可。`operation_busy` 经 RPC Unavailable 映射为 HTTP 503，并同步 OpenAPI；读取失败保留原状态，写入仍按结果未知处理，不自动重试或重放。该固定值不是 NAS 实测容量，也不是跨 Manager 或全服务并发上限。完整数据库 race、三轮定向检查及本地 3852 项前端单元（140 文件）、lint、类型检查、构建与包预算通过。
- **目录补充读取的取消语义**：[10368c2](https://github.com/Kkwans/nas-control-plane/commit/10368c23cd14826bf61188987d6ae7c2315d006a) 不再将统计或定义读取中的取消、超时伪装为部分成功，保留既有超时分类与普通可选信息错误的兼容行为；覆盖包装错误、成功扫描期间取消、空目录及连接池/许可回收。基线复现失败后，完整数据库 race（8.981 秒）、独立十轮定向 race、构建和 vet 通过。
- **行写入回执不能伪装成功**：[38b14ba](https://github.com/Kkwans/nas-control-plane/commit/38b14ba709f14e430be876aa245ffb8e569442be) 修复忽略 `RowsAffected` 错误的问题，只接受 0 至 2^53−1 的整数，合法零值仍保留；无效、缺失或读取异常的回执经 RPC Internal 映射为 HTTP 502，并提示写入可能已生效、需要只读核对。仅隔离回执读取时的 panic，不包围执行、不承诺回滚，也不重放；稳定错误不携带底层敏感异常。完整数据库 race（15.055 秒）、独立三轮定向检查、RPC/HTTP/OpenAPI 回归及本地 3855 项前端单元（140 文件）、lint、类型检查、构建与包预算通过。[CI 38786446718](https://github.com/Kkwans/nas-control-plane/actions/runs/38786446718) 全部适用检查通过：3855 单元/140 文件、453 浏览器用例及 Go/Compose，发布跳过；此时尚无字体时序屏障，不能据该次通过认定偶发问题已修复。
- **保留重复失败与可核验原因**：此前 11351ce 的 [CI 38784406471](https://github.com/Kkwans/nas-control-plane/actions/runs/38784406471) 与 10368c2 的 [CI 38785361652](https://github.com/Kkwans/nas-control-plane/actions/runs/38785361652) 均为 452 项浏览器通过、一项 390px/200% 字号摘要可见性失败；3852 单元/140 文件及 Go/Compose 通过。首份失败制品中的 376 项源码哈希与提交一致、CI 工作树干净；捕获前半段仍记录根字号 15px、摘要 y=471px/h=44px，同一阶段稍后的目标已变为 y=1162px/h=60px，后续根字号为 30px，说明滚动发生在字号重排之前。该证据解释本次捕获混入旧字号布局；原 #366 没有制品，仍不能反推其具体失败现场。
- **只修正测试就绪条件**：9aa04d1 先等待计算字号符合预期，再等待字体就绪，之后沿用原单次滚动；对截图前、截图后及完整 axe 后三阶段增加字号与目标控件几何一致断言。没有修改产品 CSS、重复滚动兜底或缩小可见性、命中及 axe 检查。[CI 38787383741](https://github.com/Kkwans/nas-control-plane/actions/runs/38787383741) 全部适用检查通过：3855 单元/140 文件、453 浏览器用例及 Go/Compose，发布跳过。该次通过验证了新增就绪条件下的回归结果，不证明所有潜在偶发问题永久消除。
- **最终制品与字号证据**：[制品分片 6](https://github.com/Kkwans/nas-control-plane/actions/runs/38787383741/artifacts/11998344883)、[分片 7](https://github.com/Kkwans/nas-control-plane/actions/runs/38787383741/artifacts/12000105132) 与[分片 8](https://github.com/Kkwans/nas-control-plane/actions/runs/38787383741/artifacts/11999246440) 中 40 张实际 PNG、40 份捕获状态及六份最终请求记录已复核；376 项源码哈希与 9aa04d1 一致，CI 工作树干净，PNG 与状态哈希匹配。32 组字号捕获的三个阶段均保持预期 15px/30px；390px/200% 摘要三个阶段均为 x=31px、y=503.03125px、宽 328px、高 60px，位于视口内并通过中心命中，实际图片可见聚焦摘要与长 ID 折行。六份请求记录全部为 GET，历史请求序列与预期一致，无未知请求、页面错误或任务/历史流订阅。完整 axe 零违规，仍保留 color-contrast、heading-order incomplete 人工审查项，不代表全面无障碍合格。

以上为源码、模拟驱动、临时库及 CI 浏览器 fixture 的隔离验证；真实 NAS、Docker/Agent socket、数据库 TLS 握手及原生多会话仍未验收。没有部署、发布或生产操作。


## Compose 可信回执、键盘确认与日志保留预算（2026-10-15 20:43 UTC）

最新已推送代码为 [597c398](https://github.com/Kkwans/nas-control-plane/commit/597c398b88251ff2cf1ac8b0b609d97ff92827db)。本节接续 4cceab8 之后的十个提交，保留夹具失败、真实产品缺陷与最终复验的区别；新增测试或本地通过不替代实际浏览器证据。

- **数据库目录收集预算**：[1e42f08](https://github.com/Kkwans/nas-control-plane/commit/1e42f08a47e6f9615f94afa70fd7e394ac510bbd) 在 Agent 收集表、列、默认值及补充元数据时增量核算既有 1 MiB 保守预算，超限停止后续查询，明确失败且不返回部分目录。保留普通可选统计错误、取消清理和既有行读写列解析行为。完整数据库 race、三轮定向回归、RPC/HTTP/OpenAPI、构建及 vet 通过；[CI 38791510425](https://github.com/Kkwans/nas-control-plane/actions/runs/38791510425) 全部适用检查通过：3855 单元/140 文件、453 浏览器用例及 Go/Compose。该预算不限制驱动单值分配、数据库内部查询工作量或总堆内存，不是 NAS 性能结论。
- **Compose 后端明确状态证据**：[a969171](https://github.com/Kkwans/nas-control-plane/commit/a9691719d7615d6087a6a3a633bed6291fe93ac4) 拒绝空记录、缺失或矛盾状态、重复身份及歧义字段，保留数组/逐行 JSON、同名副本与旧状态兼容。启动/重启仅由明确 running 确认；停止仅由明确 exited、created、dead 或兼容 stopped 确认，未知或过渡状态不再伪装完成。不增加命令、查询次数或重试。原代码回归复现失败后，完整 Compose 测试、20 轮 race、构建与 vet 通过；[CI 38792793513](https://github.com/Kkwans/nas-control-plane/actions/runs/38792793513) 全部适用检查通过。一次非运行观察不证明健康停止，任何一次读回也不证明服务健康、稳定、配置完整性或重启次数。
- **前端回执归属与未知结果**：[0d66cda](https://github.com/Kkwans/nas-control-plane/commit/0d66cda56271b3a0fa6755b54698c07b77501313) 冻结项目、动作、工作目录和配置文件，严格核验回执并仅投影已知字段；使用精确 200、禁止自动重定向的专用传输，接受合法同名副本，不以旧清单容器数量或服务名绑定实际 Compose 目标。执行后可能出现的异常归为结果未知，提示只读核对，不展示底层重试建议。只读刷新失败不覆盖原分类，迟到响应受会话/视图归属保护。本地 3932 单元/140 文件、lint、类型检查、构建与包预算通过；不增加 Job、轮询、未知锁或跨请求幂等。
- **日志原始输出与去重窗口有界**：[50f1c6d](https://github.com/Kkwans/nas-control-plane/commit/50f1c6d9514b98c0671b98223f3cb48376674d7d) 将每次历史 journalctl 原始 stdout 保留量限制为 2 MiB；溢出取消子命令并等待收尾，整页失败，替代 Runner 也在解析前检查长度。保留调用者取消优先级及普通退出码兼容。完整 journal race、聚焦重复测试、vet 和构建通过。[071a518](https://github.com/Kkwans/nas-control-plane/commit/071a518a438069ca5b346432524a8065bbc21877) 将日志 SSE 无限已见集合替换为每连接精确 ID FIFO，最多 2048 个 ID、256 KiB ID 内容；克隆保留字符串并清空淘汰槽位。近期重复仍被抑制，游标继续推进；窗口外或单个超过预算的 ID 允许再次发送。纯内存结构与 fake Agent SSE race、独立重复回归、vet 和构建通过。两项分别是原始输出与保留 ID 内容的预算，均不是全局内存、并发或响应大小上限。
- **保留首次失败及夹具纠正**：0d66cda 的 [CI 38794048232](https://github.com/Kkwans/nas-control-plane/actions/runs/38794048232) 以及后续 50f1c6d 的 [CI 38794735113](https://github.com/Kkwans/nas-control-plane/actions/runs/38794735113)、071a518 的 [CI 38795538161](https://github.com/Kkwans/nas-control-plane/actions/runs/38795538161) 均为 452 项浏览器通过、五项失败。两项完成回执捕获因暂停页面时钟阻塞完整 axe；两项未知结果用例向没有订阅的页面注入 SSE，变化清单未送达；既有生命周期用例仍期待旧成功文案。[d2a8003](https://github.com/Kkwans/nas-control-plane/commit/d2a800334bccb3734f8e4da8d50ea2fe17b120d8) 保持 axe 计时器运行，以原生 hover 暂留提示，改用真实只读 GET 送达变化清单并修正旧文案断言。浏览器验证确认时的原配置及精确 POST，确认期间配置变化的冻结保护由组件回归证明，不能把两类证据混写。其 [CI 38797308036](https://github.com/Kkwans/nas-control-plane/actions/runs/38797308036) 为 455 通过、两项真实成功消息对比度失败，未关闭 axe 或降低门槛。
- **确认范围、对比度与中性标题**：[aec8520](https://github.com/Kkwans/nas-control-plane/commit/aec85202265a7892301fc148658f854e4cc6da1c) 将确认框容器数明确为最近清单观察，说明实际范围由执行时 Compose 配置决定，并仅为 Compose 确认框增加 44px 最小触达区域、标题避让和可见焦点。[8fe5840](https://github.com/Kkwans/nas-control-plane/commit/8fe5840bef11983f2d154496f5f665e22f60ad51) 修复实际 14px 成功文字在浅绿背景上的 4.46:1 对比度，局部绑定既有深绿 token，静态比值为 5.92459:1。[cbdaa9f](https://github.com/Kkwans/nas-control-plane/commit/cbdaa9fac94eac671afcf2c8cd17147a1ffcd41e) 将列表和抽屉的未知/未完成提示标题统一为“项目操作需核对”或“容器操作需核对”，Compose 说明不再声称逐容器执行；不改变回执分类或动作逻辑。
- **原生键盘缺陷与修复**：aec8520 的 [CI 38798198042](https://github.com/Kkwans/nas-control-plane/actions/runs/38798198042) 为 455 通过、两项失败：1440px 的 Enter 被 tooltip 拦截，确认框未打开；390px 已通过 Tab/Shift+Tab 焦点圈，但取消后未归还原触发点。两份账本均零写请求，未到成功提示捕获阶段，因此该轮不能用于否定或确认对比度修复。8fe5840 的 [CI 38800527634](https://github.com/Kkwans/nas-control-plane/actions/runs/38800527634)、cbdaa9f 的 [CI 38801221688](https://github.com/Kkwans/nas-control-plane/actions/runs/38801221688) 仍保留 455 通过、两项键盘/焦点失败。[597c398](https://github.com/Kkwans/nas-control-plane/commit/597c398b88251ff2cf1ac8b0b609d97ff92827db) 让项目 tooltip 保留按钮原生 Enter/Space 激活；取消/关闭后释放自己持有的 pending，等待 DOM 更新，再经会话、视图、新操作及目标可用性检查恢复焦点。同步防重复门禁保留，失效请求和已确认操作不主动抢回焦点。完整 3970 单元/142 文件、lint、类型检查、构建及包预算通过，原浏览器断言保留并新增 Space→Escape 证据。
- **最新完整 CI**：[CI 38801843808](https://github.com/Kkwans/nas-control-plane/actions/runs/38801843808) 已确认终态成功：3970 单元/142 文件、457 浏览器用例及 Go/Compose 检查通过，ARM64 发布步骤跳过。以下实际制品复核对应同一 597c398 提交。
- **实际图片、哈希与键盘账本**：[Compose 专项制品](https://github.com/Kkwans/nas-control-plane/actions/runs/38801843808/artifacts/12004254960) 的 391 项源码哈希与该提交一致，42 项账本关联制品哈希及 14 张 PNG 哈希匹配。14 张实际 PNG 和 14 份完整 axe 捕获均已复核，覆盖 390/1440 的停止/重启确认、停止/启动完成观察及错误项目、错误动作、VERIFY_FAILED 未知结果。目标文字、弹窗、核对提示和桌面 tooltip 可见且未越界；四个确认框的关闭、取消、确认按钮分别为 44×44、60×44、88×44px，关闭区域与正文间距 8px、标题与关闭区域间距 16px。16 次 Enter/Space 取消、关闭或 Escape 周期全部完成并恢复原焦点，128 步 Tab/Shift+Tab 均留在弹窗内、焦点可见且描边至少 2px；这些取消周期零请求。四份最终账本无未知请求或页面错误，14 组捕获在截图与 axe 期间请求保持不变。
- **写入次数与无障碍边界**：每个宽度的完成观察用例仅有一次停止 POST 和一次启动 POST；未知结果用例分别显式触发三个场景，各一次重启 POST，不是自动重放。完整 axe 的 14 份捕获均零 violation，但每份仍有 aria-valid-attr-value（一处弹出层引用）及 color-contrast（10–11 处，涉及渐变、短文本或重叠）incomplete。四张成功提示 PNG 可见预期深绿文字和浅绿背景，纯色对比约 5.9246:1；390px 启动提示文字仍被 axe 标记部分重叠、需人工审查，不能称自动对比度检查全部通过。静态像素、几何与键盘复核不替代屏幕阅读器、缩放、其他浏览器引擎、动态状态或全面无障碍验收。

以上为源码、模拟驱动、临时 SQLite、fake 执行器/Agent 与 CI 浏览器 fixture 的隔离验证。没有真实 journalctl、Docker/Compose、NAS、数据库 TLS 握手或原生多会话实机验收，没有部署、发布或生产操作。

## Docker 容器键盘与详情体验收尾（2026-10-15 22:26 UTC）

- [2c222b6](https://github.com/Kkwans/nas-control-plane/commit/2c222b64081c4e3791e3279029b6d58c03d655c6) 恢复单容器生命周期原生键盘激活、取消后归还焦点，并为确认框提供44px操作区；[2ae07ed](https://github.com/Kkwans/nas-control-plane/commit/2ae07ed4555b454917d80714f81f546f780dbb2c) 恢复列表与项目抽屉详情入口的原生激活，不改变操作请求和权限。首次容器专项实际 CI 保留459通过/2抽屉失败：完整 axe 发现缺失标题关联、健康状态对比度4.39:1和标题层级缺口，不能把中断前未执行取消解释成焦点恢复失败。
- [3e5d3d8](https://github.com/Kkwans/nas-control-plane/commit/3e5d3d817e9459c4f704e7ec891b1c984650431c) 正式消费两个抽屉的标题ID、恢复h2，并限制项目副标题选择器，避免覆盖状态语义色。[CI 38809245522](https://github.com/Kkwans/nas-control-plane/actions/runs/38809245522) 全绿：4019单元/144文件、461E2E。四个生命周期场景共32次取消全部完成并恢复焦点；八份完整axe零违规，仍保留incomplete。
- [f14be5e](https://github.com/Kkwans/nas-control-plane/commit/f14be5ec24f1854ced72a0de7b24793f3e61faa6) 新增390/1440×列表/项目抽屉四个只读详情浏览器场景。首次[CI 38809345663](https://github.com/Kkwans/nas-control-plane/actions/runs/38809345663) 保留461通过/4失败：INFO文字4.11:1及传送到body后的重复banner。八次键盘激活、Escape关闭与恢复均完成，每次精确两个GET，零写请求；axe软断言保持失败同时保留后续独立证据。
- [3923d0a](https://github.com/Kkwans/nas-control-plane/commit/3923d0a042a974d4f1b6edfa1bab680f408b4745) 使用既有Teleport→未命名section→原生ElDrawer模式保留标题、关闭和焦点合同，INFO改局部strong色，静态对比度5.76:1。[CI 38812226434](https://github.com/Kkwans/nas-control-plane/actions/runs/38812226434) 全绿：4022单元/144文件、465E2E。395源码散列与24制品散列匹配，八张实际截图目检无目标裁切重叠，完整axe零违规。实际关闭按钮仍为32×22，保留为下一片明确缺口。
- [6a31269](https://github.com/Kkwans/nas-control-plane/commit/6a312696996bc50a4addf8388f9e6d49078763e3) 将详情原生关闭按钮局部扩大到44×44，标题间隔12px，保留2px可见焦点环。新增原生按钮Enter/Space和角内点击关闭证据，原Escape循环不删除。[CI 38815054495](https://github.com/Kkwans/nas-control-plane/actions/runs/38815054495) 全绿：4025单元/145文件、465E2E，Go/Compose通过，发布跳过。
- 最新[证据分片2](https://github.com/Kkwans/nas-control-plane/actions/runs/38815054495/artifacts/12010783370)与[分片3](https://github.com/Kkwans/nas-control-plane/actions/runs/38815054495/artifacts/12010992959)的396源码散列及60制品散列全部匹配精确提交，工作树干净。20循环全部完成并恢复焦点，合计40目标GET且每循环详情/日志各一次，零写请求、拒绝请求及页面错误；20捕获期间请求稳定。所有关闭区域44×44、标题间距12px，80个角内采样点均命中，无根节点横向溢出；20份焦点均可见且2px solid。20张PNG分为5组不同字节图，逐组目检无标题、信息卡或日志裁切重叠。

20份完整axe均零violation，但仍各有aria-valid-attr-value（一节点）和color-contrast（十节点）incomplete，涉及底层页面弹出层引用、渐变和短数字，不能据此宣称全面无障碍认证。初始触发器通过focus()定位，未证明从页面起点完整Tab可达性；未覆盖屏幕阅读器、缩放或其他引擎。移动全页截图在844px视口以下显示底层页面，属于固定抽屉与全页截图范围，不是已确认的视口内泄漏。全部验证仍为隔离组件与CI fixture，未进行NAS实机操作或验收。

## 镜像任务结果与当前引用核对（2026-10-15 23:18 UTC）

- [ed52973](https://github.com/Kkwans/nas-control-plane/commit/ed5297303b3f7642a14f83ac1dca0ebf00023b35) 修复拉取完成回执缺少目标归属检查。纯fake基线证明请求alpine:latest但返回nginx:latest/completed可错误标记任务完成及制品存在。现在普通/流式RPC和HTTP任务入口均按现有规范化合同精确绑定冻结请求；错配、空白、未完成回执保持interrupted及artifact unknown，不自动重拉。进度、取消/超时、已有终态和仅重存待保存结果的行为保留。聚焦race×20、独立复验、构建、vet、OpenAPI测试通过；[CI 38818194191](https://github.com/Kkwans/nas-control-plane/actions/runs/38818194191) 全绿：4025单元/145文件、465E2E及Go/Compose，发布跳过。
- [b7c4eab](https://github.com/Kkwans/nas-control-plane/commit/b7c4eab6657273803a448e4fc877c99577bbfc79) 单独修正当前引用存在性：Docker Hub默认域/index.docker.io/library别名按官方规则统一，前端同时查RepoTags和RepoDigests。保留第三方registry、端口、大小写、完整tag+digest，不补latest；无效或不在保守子集的任务引用为unknown。Go复用已安装reference v0.6.0，TS专用纯比较函数由86条共享fixture约束；独立10406组定向及固定种子差分输入无差异，不代表穷尽所有字符串。P1严格回执比较未放宽，当前tag存在不能证明仍是原历史镜像ID。
- 基线两端均复现别名错判；修复后4112单元/145文件、Go聚焦race、lint、类型检查、构建及预算通过。[CI 38820801024](https://github.com/Kkwans/nas-control-plane/actions/runs/38820801024) 精确SHA终态全绿：4112单元、465E2E及Go/Compose，发布跳过。前端无视觉改动，不重复将旧截图算作新专项视觉证据。依赖只将既有reference v0.6.0标注为直接依赖，版本与go.sum未变。

以上仍为纯fake/structpb/临时存储与CI fixture验证，无真实镜像拉取、Docker或NAS实机验收。
