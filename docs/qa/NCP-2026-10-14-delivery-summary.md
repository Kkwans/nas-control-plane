# NCP 接手以来的累计改动

检查点：2026-10-14 05:42 UTC，分支 `dot/ncp-modernization`。比较范围：[接手基线 5b81557](https://github.com/Kkwans/nas-control-plane/commit/5b81557068a9a44cae3b06ac6207438a399e8ef7) → [当前代码 43d1742](https://github.com/Kkwans/nas-control-plane/commit/43d1742995ed75e27a4e25cf2e0c281cc96f2dae)。

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

## 验证状态与尚未完成的部分

- 最新 **43d1742** 的 [CI 38517205338](https://github.com/Kkwans/nas-control-plane/actions/runs/38517205338) 已完整通过：3013 项前端单元、400 项浏览器用例及 Go/Compose 检查；前一代码版本 **f2cce02** 的 [CI 38515767652](https://github.com/Kkwans/nas-control-plane/actions/runs/38515767652) 亦全绿。
- **f2cce02** 的 SQL 四张截图覆盖 390/1440px 未确认结果及再次执行确认；源码与图片哈希已核对，完整 axe 扫描零违规，但保留两类 incomplete，不能据此声称全面无障碍合格，也不覆盖 43d1742 新增的模式说明。
- 浏览器使用隔离 API 数据；部分集成使用临时 SQLite、文件和受控子进程。真实 NAS、Docker、PostgreSQL/MySQL及原生多会话尚未验收。**代码已提交，不代表部署或发布；本轮没有部署或发布。**
- 下一步先闭环最新 CI，继续可隔离验证的开发；实机验收留待另行授权。仍需推进 Agent 专用访问身份、数据库 TLS 策略及更完整的跨引擎 SQL 自动识别；慢操作统一任务化、跨页面幂等回执和一致性备份恢复仍待实现。现有页面防重复保护不能替代服务端幂等，Compose 内容核对也不能证明应用运行健康。
