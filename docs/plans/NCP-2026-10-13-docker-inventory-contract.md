# Docker 清单共享响应边界与恢复

2026-10-13，基线22d08db；M29实施方案。

## 已确认问题与边界

GET 与系统 SSE 各有独立浅层 Docker 清单守卫，只检查对象及数组，未验证行与嵌套端口。源码派生的内存反例包括null容器、缺失state、null端口/端口行、数字protocol、null项目/configFiles，进入页面消费者后可触发TypeError。它证明异常响应的崩溃路径，不证明正常Go采集器会发出坏记录。

Go合同见internal/docker/inventory.go:30–81；实际采集器通常分配/规范化集合，但成功provider/RPC/cache仍可保留nil切片，故显式null集合必须兼容。五个可选丰富字段保持可选，零端口、负退出码、空字符串、Go零时间均合法。

## 最小解码合同

- 顶层必需collectedAt字符串、engine对象、containers/projects集合。
- engine三个版本/OS/架构字符串；四个容器/镜像计数字段为有限非负整数。
- 容器必需id/name/image/state/status/createdAt/projectId/projectName/serviceName字符串和ports集合；startedAt/finishedAt可选字符串，exitCode可选有限整数，restartCount可选非负整数，health可选none/starting/healthy/unhealthy。
- 端口hostIp/protocol字符串，privatePort/publicPort为0–65535整数。
- 项目id/name/workingDirectory字符串，kind为compose/standalone，state为running/stopped/degraded，configFiles字符串集合，containerCount/runningCount为非负整数。
- 仅containers/projects/ports/configFiles的显式null归一化为空数组；缺失集合、坏元素和错类型拒绝整个清单，不静默丢行。不变更输入，只投影已知字段，接受附加字段。
- 不发明ID、路径、名称格式、计数一致性和跨资源关联限制。容器state和protocol仍为开放字符串；时间仍由既有新鲜度逻辑处理，不增加Date解析过滤；OpenAPI既有date-time格式保留，浏览器容错范围不要求削弱服务端线协议说明。

## 读取与流式恢复

GET使用共享解析器，维持fetcher、signal、同源选项及HTTP错误；异常清单返回稳定DOCKER_INVENTORY_RESPONSE_INVALID，不渲染原始体。

SSE先保持既有整个信封、summary、errors校验，再独立解析Docker部分。坏的已存在Docker（包括null）只移除此部分并追加稳定错误；合法summary和其他错误保留，EventSource不关闭。既有store保留最后合法清单，后续合法且新鲜的快照才清除资源错误并更新基线；坏清单不得推进时间戳。整个信封或summary异常的既有行为不在本片改写。

DockerView当前用全局connectionState判断首次加载。若先加载过capabilities，首个清单GET失败仍会处于degraded且一直显示加载。补充资源级loading/error呈现，区别首次等待、首次失败可重试、合法空清单与旧快照刷新失败；不改变全局资源状态聚合、不新增长轮询、不自动重放写操作。复用既有强刷新屏障、会话和代次守卫。

M27创建未知结果的五字段投影及禁止提交锁继续保留。任何合法清单仍不能证明那次创建成功、失败或允许重试。

## 验证与交付

1. 表驱动纯解码：全部必需字段/错类型/嵌套坏行、数值范围与有限性；合法null/空集合、空字符串、负exitCode、零端口、未知state/protocol、无可选字段、输入不变。
2. GET集成：稳定错误、信号与传输选项、正常归一化。
3. fake EventSource：合法summary+坏Docker继续流且有独立错误，下一合法Docker在同一流恢复；缺省Docker与非法Docker不同。
4. store/view：先capabilities后首次清单错误、保留旧数据、只读重试、坏流后恢复、旧/同时间快照不推进、会话失效无迟到写回。
5. OpenAPI同步必需字段、nullable集合与端口范围；纯schema正反例及完整规格验证。
6. 后续隔离浏览器验证390/1440错误/旧数据/恢复及axe/实际截图；本地只做纯测试与静态构建，不启动服务或访问真实Docker/NAS。

单片审查精确diff、测试后中文提交推送，再核验远端与CI；所有未完成的实际验证明确保留。

## 实施与独立验证（20:56 UTC）

共享解析器已用于GET与SSE，逐行拒绝异常、仅规范化四种明确null集合；SSE的坏Docker不再关闭合法summary所在的流。OpenAPI补齐字段与范围，保留原有date-time格式。创建结果未知路径仍额外投影最小展示字段。

资源状态新增inventoryRefreshing/inventoryErrorCode/inventoryStale，初始等待不冒充失败，首读失败可单独重试、旧快照失败保留原数据和时间。主审另发现GET-only路由原本不启动新鲜度时钟，已复用现有单一本地时钟及可见性/会话清理；空作用域保持offline、不建立EventSource、不轮询，经过16秒可自然显示过期。相同作用域重复调用不增加时钟，stop/clear/logout/dispose均清理。

独立复跑190项覆盖API、store、view与M27，并通过完整OpenAPI包；主任务精确源码快照82文件/1601项单元、全量lint、类型检查、构建及包预算通过，完整OpenAPI验证与路由一致性通过。九项新内存浏览器fixture已完成静态检查与发现，覆盖capabilities先行、错误/空/旧快照/只读重试、同源流恢复和GET-only过期，计划390/1440各七组真实PNG/状态/完整axe；运行与像素验收待新CI，不以单元结果替代。
