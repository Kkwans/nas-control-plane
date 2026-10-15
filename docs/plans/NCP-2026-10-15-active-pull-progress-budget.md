# 活跃镜像拉取进度保留预算

基线：583338ddcbf4818a8f33faeefe5abe5659a57e50。限定服务端新接纳的拉取进度，不改 queue、持久历史摘要或 recovery 协议。纯 fake 基线复现2048独立层、每状态1KiB，完整任务JSON为2,233,503字节。

## 批准合同

- 每任务最多128层，ID最多128 UTF-8字节；超长或非法UTF-8 ID整条拒绝，不截断ID，不保留被拒绝字符串。空ID仍映射既有status槽，不新增身份语义。
- Status最多256字节，保留合法UTF-8前缀；非法编码处或跨预算码点前停止。ID与状态独立复制，短切片也不保留上游大字符串。
- 累计32KiB预留文本预算，每个新层计2×ID字节+256，分别覆盖map key、层ID与未来完整Status更新。重复ID不再消耗预算，已接纳层仍可更新。因最短非空ID也有成本，累计预算可早于128层触发（1字节ID最多127层）。拒绝不维护无限去重集合或丢弃计数。
- progressLimited为可选布尔，首次拒绝/截短置true并标dirty，随后单调保留。缺省false仅表示没有记录预算降级，绝非所有进度完整证明。不会因为遥测超限发取消、失败或成功；最终结果仍走P1严格引用归属回执与原持久化协议。
- 层字节聚合使用int64饱和避免大量极值溢出为负数。非正数字仍沿用原“保留上一值”语义。

## 持久化、API与展示

SQLite新增progress_limited INTEGER NOT NULL DEFAULT 0，通过现有可重复列迁移，旧记录默认false。Create、CAS Update、Upsert与全记录读回携带字段；pending outcome、保存重试和CAS期间新进度保持标志。历史摘要不含进度，保持原投影和合同；recovery不改。

JobSnapshot/get/list/SSE与OpenAPI新增可选progressLimited。旧库可读的layers保持原兼容范围，不对所有历史JSON添加新层数/文本schema硬限制。新进度预算写入说明。

下载卡片、任务中心及任何使用Job进度的视图在true时显示固定部分进度说明，隐藏不完整百分比、字节/总量与速度，保留执行状态、取消/pending/断流独立说明。完成表示可信执行结果，不表示曾观察完整下载统计。前端不同观察通道合并保持true粘性。

## 精确资源边界

32KiB仅是新进度保留文本预留，不是JSON、map结构、数据库、网络接收或进程堆总量。JSON字符串最多六倍转义，层JSON固定结构（两int64、键名与分隔符）每层不足100字节：32KiB×6+128×100=209408字节。进度子对象附加聚合数字、标志和最多256字节消息（最坏1536编码字节）后仍低于220KiB。此上界不包含任意reference/error、其他任务元数据、列表任务数量、临时JSON缓冲或运行时开销。单个上游RPC/structpb帧本身也不由本片设内存上限。

旧数据库巨大LayersJSON启动加载明确排除；不声称全进程内存上限。已存巨型记录的读取治理需独立切片。

## 验证

纯fake Agent、structpb既有P1测试、Recorder与临时SQLite；不运行监听器/浏览器/PTY/Docker/NAS/用户电脑/生产。覆盖字段/累计/层数边界、Unicode与非法UTF8、重复/空ID、巨串切片独立复制、CAS中新增flag、保存失败pending原子性、超限后可信/错目标/取消终态及晚帧、SQL存储体积、get/list/SSE与六倍转义/最大数值JSON。前端单元、类型检查/构建及Go聚焦race/build/vet/OpenAPI回归；无视觉实测声明。

### 已完成本地验证

后端定向race各10轮通过：HTTP预算/P1回执/快照/pending/取消/CAS/SSE；Store progressLimited迁移与持久化；Agent structpb与Docker回执归属。go build ./...、httpapi/controlstore/agentsocket go vet、OpenAPI全测试通过。补充文件体积测试后预算race再通过。

同形2048帧现保留120层，LayersJSON为38,301字节，临时SQLite文件相对已建schema增长36,864字节（单次fixture观测，不是数据库全生命周期上界）。64个128字节ID、256字节NUL状态和最大int64的完整fixture快照约201,928字节；127个短ID、NUL状态及极值快照208,058字节。两者低于220KiB进度子对象预算，并在更大完整fixture上验证；时间戳小数位可能导致总长微小变化。

前端最终9文件306项单元回归通过（解码、删除回执、两个merge、下载卡片、任务中心及Docker Hub）；vue-tsc、13个改动文件定向ESLint、生产构建与540KiB包预算通过。独立审查复跑HTTP预算race、SQLite、OpenAPI及相关前端185项通过，无剩余阻断。未做浏览器视觉验证；未提交、部署或修改共享QA，保留既有__pycache__。

根审查后最终全量前端4150项/146文件通过；HTTP预算race另三轮、Store迁移与完整OpenAPI复验通过。新受限状态尚未取得真实浏览器截图，独立只读UI专项后续验证，不把既有浏览器回归视为该新状态覆盖。

## 独立浏览器证据专项

新增14例390/1440像素静态fixture：受限running/completed/pending/cancel-requested/cancel-unknown及省略/false兼容，每例检查下载卡和任务中心。保留完整axe、实际文字Range/容器裁剪、截图及源码/图片散列、前后请求稳定和最终失败账本；GET精确来源白名单拒绝所有写、外部来源、未知API和真实WebSocket。仅隔离fixture中精确15秒的自动刷新计时器，其他计时器与axe保持运行；元数据记录抑制次数，明确不验证真实轮询或实际拉取。正常样式包含ellipsis不等同实际裁剪，使用几何判定而非样式值硬禁。ESLint、strict TypeScript、14例发现与独立静态审查通过；真实28张截图及无障碍结论等待远端CI。
