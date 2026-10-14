# 创建容器挂载路径身份：保留名称空白

日期：2026-10-14。基线：78df508。本切片只修复目录浏览、创建表单和挂载归一化中的路径空白丢失，同步 OpenAPI；不改 Docker 权限、路径白名单、设备路径、运行时挂载策略或默认启动命令。

## 问题与方案

Linux 上 `/selected` 和 `/selected ` 可以是不同目录。原链路在文件浏览 `Request.Normalize`、创建表单 `buildInput`、Docker `normalizeMounts` 使用通用 `TrimSpace`，会将选中的路径变成另一个名称。来源和目标同时受影响。

- 文件浏览不去除路径首尾空白，仍要求绝对路径并沿用 `filepath.Clean` 对分隔符、点段的处理。
- 表单与 Docker 归一化原样保留 bind 来源及全部挂载类型的目标路径。`/data` 与 `/data ` 不再误判为重复目标；完全相同的目标仍拒绝。
- Docker 卷名不是文件路径，继续去除首尾空白并执行既有卷名校验；tmpfs 仍无需来源，切换到 tmpfs 清空旧 bind 来源。
- 前导空白不会被修剪成合法绝对路径。后端沿用以 `/` 开头与原有路径校验，前端也在生成确认预览前拒绝前导空白的 bind 来源和所有目标。
- RPC 的字段编码、HTTP 查询解析与 JSON 数据结构无变化。路径 RPC 测试明确使用 `/bin/true` 参数，隔离另行处理的默认启动命令空值问题。

## 证据与边界

- 前端新增真实 Vue 抽屉、Element Plus 选择器/弹窗及 API 编解码的组件测试，仅 mock fetch。点击目录、进入目录、返回选择、确认预览到 POST JSON 均验证原始路径；同一请求包含 `/selected` 与 `/selected `、`/data` 与 `/data `，覆盖仅创建及创建并启动。
- tab、NBSP、全角空格的尾部名称同样进入 GET、选择、预览和 POST；前导空白及纯空白拒绝、volume trim、tmpfs 省略来源另有回归。
- 文件系统测试在普通临时目录中创建五种后缀的兄弟目录，并读取每个目录自己的 marker，证明浏览没有重定向到不带空白的目录。Docker 测试验证归一化到 Moby 请求结构的路径；RPC 只运行 codec 和直接 service 调用，不启动 listener。
- 这些是分段隔离证据，未启动真实 HTTP/RPC 服务、Docker Engine、挂载、NAS、用户电脑、浏览器或 helper。未运行含 listener 的 agentsocket 全套，也未运行浏览器 E2E；不表示真实容器已创建或部署完成。
- JSDOM 的字符串和 DOM 断言不证明尾空白在实际浏览器中肉眼可辨识。现有展示可能折叠空白，此次不新增转义显示或标记。
- 文件浏览仍允许既有点段清理，挂载仍要求既有 clean 路径；权限变化、符号链接与 TOCTOU、Docker Engine 自身的路径处理和目标平台文件系统差异均不在本次运行验证范围。

## 验证记录

前端与契约验证已通过：

- `pnpm exec vitest run src/components/CreateContainerDrawer.pathIdentity.test.ts --maxWorkers=2`：10/10。
- `pnpm exec vitest run --maxWorkers=2`：122 个文件、3531 个用例通过。
- `pnpm lint`、`pnpm typecheck`、`pnpm build` 全通过。生产包最大 chunk 518.0 KiB，低于 540 KiB 预算；保留 Vite 的既有大于 500 kB chunk 提示。
- `go test -race -count=1 ./api/openapi`、`go vet ./api/openapi` 通过。

后端验证已通过：

- `go test -count=1 ./internal/docker ./internal/filesystem` 及同命令加 `-race`：67 + 11 个顶层测试通过。
- agentsocket 采用锚定名称白名单运行 `go test -count=1 -v` 及 `-race`，覆盖本批 4 个路径 codec/direct-service 测试、既有相邻创建校验和另行修复的 command 回归，共 10 项；不运行任何 `OverGRPC`、bufconn 或 listener 测试。
- 最后给路径拒绝测试的 raw RPC payload 显式填写合法 command 后，再单独运行 4 个路径用例及 race。白名单为 `^(TestFilesystemCodecPreservesPathWhitespace|TestContainerCreateCodecPreservesMountPathWhitespace|TestFilesystemPathIdentityRejectsLeadingWhitespaceBeforeProvider|TestContainerMountPathIdentityRejectsLeadingWhitespaceBeforeProvider)$`。
- `GOOS=linux GOARCH=amd64 go build ./...`、`go vet ./internal/docker ./internal/filesystem ./internal/agentsocket` 与 `git diff --check` 通过。

本切片只有上述路径改动及对应测试/文档；默认 command 的实现和测试属于独立修复，不包含在本切片文件清单中。源码同步与本地验证均不代表已提交、推送或部署。
