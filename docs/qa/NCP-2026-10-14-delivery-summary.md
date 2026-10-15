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
