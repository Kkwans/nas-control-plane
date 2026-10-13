# M10-A：浏览器 HTTP 写请求来源防护

## 范围与实现

源代码切片，不代表 NAS 已部署。`/api/v1` 的来源中间件在认证之前执行，包括 bootstrap、login、logout、所有业务 POST/PUT/PATCH/DELETE；请求 ID 中间件仍在最外层。使用 Go 1.26.4 的 `http.CrossOriginProtection`，没有可信来源白名单或路由豁免。先拒绝重复、空、null 或不符合单个 `http(s)://host[:port]` 形式的 Origin，再交给标准库判断。

拒绝统一返回 HTTP 403、`HTTP_CROSS_ORIGIN_REJECTED` 与 `requestId`。不会读取请求体、操作会话、创建 Job 或调用 Agent；错误与日志不回显来源或凭据。OpenAPI 为每个非安全 API 操作声明共享 403。前端沿用稳定错误码与消息，不把此错误当作 `AUTH_UNAUTHORIZED`，不自动重试写请求。

## 兼容契约

- GET/HEAD/OPTIONS 不受写保护限制，仍服从既有路由及认证；不新增 CORS/OPTIONS 路由。`/healthz`、`/readyz` 不受影响。
- `Sec-Fetch-Site: same-origin` 或 `none` 在 Origin 语法有效/缺省时按标准库允许；same-site、cross-site 和未知值拒绝。
- 没有 Fetch-Metadata 时严格比较 Origin 的 authority 与请求 Host，包含端口。普通局域网 HTTP 常走此分支；同 hostname 的其他端口不能触发写操作。
- 此回退不比较 scheme，不承诺大小写或显式默认端口的规范化。不是完整 scheme/host/port 校验，也不是 Host allowlist、DNS rebinding 或 TLS 防护。
- 同时缺少 Origin 和 Fetch-Metadata 时允许继续，保持 CLI 兼容，但依然要求原有会话认证。代理不得同时移除这些头；缺头旧浏览器也会走这一兼容路径。
- 无 body 动作与 multipart 图标上传保持原有行为。M10-B在具体JSON decoder实施Content-Type/415校验，详见末节；无body与multipart不套用JSON要求。
- WebSocket 保留 coder/websocket 默认 Origin 检查；HTTP GET 放行不豁免 WS 校验，也不把本次 JSON 错误契约套用于 WS handshake。

## 代理和开发环境

nginx `/api/` 与既有 WS 都使用 `$http_host`，Vite `/api` 使用 `changeOrigin: false`，保留用户访问的外部 Host 及非默认端口，例如 `nas.example:8760` 或 `[::1]:8760`。不得把 Host 改成内部 `server:8750`，不得改写 Origin 以绕过检查。每层额外反向代理都应保留相同外部 authority 与原始浏览器来源头。

应用不读取 Forwarded、X-Forwarded-Host、X-Forwarded-Proto、X-Forwarded-Port 来推导可信来源。HTTPS 外层终止不改变 authority 回退的规则；真实多层代理部署尚未核验。不要用可信来源豁免掩盖端口丢失。当前 Vite 未提供 WS 代理，此切片不宣称修复开发终端代理。

## CLI 示例

凭据保存在用户自己准备的私有文件中，cookie jar 应限制为仅本人可读。不需要添加 Origin 或 Referer；若主动添加 Origin，仍必须符合相同校验。

```sh
umask 077
curl -H 'Content-Type: application/json' --data-binary @credentials.json \
  -c session.cookies http://nas.example:8760/api/v1/auth/login
curl -b session.cookies -X POST \
  http://nas.example:8760/api/v1/docker/containers/CONTAINER_ID/actions/restart
curl -b session.cookies -X POST http://nas.example:8760/api/v1/auth/logout
```

示例是契约说明，不是本次执行的命令，也未接触真实凭据或 NAS。

## 验证与边界

确定性测试使用 recorder、临时认证数据库、fake Agent 和无真实监听端口的 ReverseProxy transport：来源矩阵（含 IPv6、异常头、端口与 scheme 限制）、动态遍历全部非安全 API 路由、403 JSON/日志关联、拒绝前不读 body/不访问认证或业务、拒绝 bootstrap 不建账号、拒绝 logout 不销毁会话、同源及无来源的登录/写入/退出、匿名/已退出 401、健康与就绪路径、multipart/body 原样传递。OpenAPI 全量 Validate 和路由/403 覆盖测试防止新入口遗漏。前端测试检查稳定码及不重试。

代理 fixture 与配置断言不能代替真实 nginx/浏览器端到端验证。真实浏览器双端口攻击、局域网 HTTP 和额外 HTTPS 代理验收待 CI/独立测试环境执行；不访问 NAS 或真实 root PTY。

## M10-B JSON MIME 契约（2026-10-13 代码及独立审查完成）

既有Origin防护保持有效，本片补充四个JSON decoder的内容类型契约，不替代来源检查。仅在具体JSON入口校验，不将所有POST/DELETE全局改成JSON。

- 35个JSON路由接受单个application/json Content-Type，使用标准mime.ParseMediaType，允许有效参数与大小写；拒绝缺失、空白、多个字段值、逗号列表、畸形参数和未声明的+json类型。同名同值参数按Go标准库处理，不等同于重复头。
- MIME失败返回415/HTTP_UNSUPPORTED_MEDIA_TYPE及requestId，不回显原始头/body/凭据。认证、Origin及依赖预检顺序不变；401/403/503不会被全局415覆盖。正确MIME的无效JSON仍使用原模块400，数据库UseNumber精度不变。
- 普通JSON入口在读取body前检查；可选mihomo inspect例外，最多探测一个字节确认实际空体，再保留前缀和原Close。真正空体不要求JSON头；未知长度非空、错误ContentLength或空白体不能绕过校验。读取错误不调用Agent。
- 11个无body动作、GET/HEAD、SSE/WS和multipart图标上传保持原行为。Standalone项目动作原实现要求JSON，OpenAPI原optional改为required以匹配真实契约。
- 前端现有JSON请求已设置application/json，不需修改产品请求。无Origin的CLI仍允许，但curl -d默认表单类型或裸无头JSON有意变为415，必须显式添加Content-Type；不能声称所有旧CLI零破坏。

验收采用recorder、fake和请求读取计数：四decoder/35路由类型矩阵、六个auth/user错误出口、MIME拒绝无业务副作用、Origin/认证优先级、multipart与无body兼容、inspect实际空体及错误读取、前端请求头契约与OpenAPI同步。真实代理/浏览器攻击及NAS部署未在本片执行。

原LimitReader可能隐藏限额之后的尾部数据，属于独立body-size边界问题，本片不通过修改读取策略顺带宣称已修复。socket专用身份仍为部署方案，不更改真实系统权限或凭据。
