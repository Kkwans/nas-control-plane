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
- 无 body 动作与 multipart 图标上传保持原有行为。M10-B JSON Content-Type/415 校验尚未实施。
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
