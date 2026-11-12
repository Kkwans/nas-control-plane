# 镜像当前引用存在性比较

## 范围与依据

修复已完成拉取任务 `docker.io/library/alpine:latest` 对本地清单 `alpine:latest` 错报删除，以及前端漏查 RepoDigests。只读取 Agent 返回的清单；不连接真实 Docker，不新增 RPC/API，不更改持久化数据、拉取校验或 P1 严格回执归属。

依据：Docker 官方 [image tag 文档](https://docs.docker.com/reference/cli/docker/image/tag/)；项目已安装的 `github.com/distribution/reference@v0.6.0` 中 `normalize.go`、`regexp.go`、`reference.go` 与 `github.com/opencontainers/go-digest@v1.0.0`。不安装新依赖。

## 最小合同

- 独立纯比较 key，只用于当前引用是否出现在清单中；当前 tag 存在不能证明仍指向当初拉取的镜像 ID。
- Docker Hub 省略域、`docker.io`、`index.docker.io` 规范化为 `docker.io`；仅 Hub 单段仓库补 `library/`。不剥离任意 registry 的 library，不压平嵌套命名空间。
- 显式第三方域、端口、localhost、IPv6 域与域大小写保持原样；`registry-1.docker.io` 和 `docker.io:443` 不当作别名。
- tag 和 digest 全部保留，包括二者同时存在；不补 `latest`、不丢 tag，不在 tag/digest 间推断等价。tag 大小写敏感，仓库路径仅允许官方小写语法。
- 保守可比较子集：显式 tag 或完整、小写十六进制的 sha256/384/512 digest；未带版本、空白/无效引用、未知 digest 算法均不可比较。名称路径遵循官方 255 字符上限。
- Go 复用 `ParseNormalizedNamed`，不调用会丢弃 tag 的 `ParseDockerRef`。TS 只实现上述存在性子集，不作为新的拉取验证器。共享 JSON 表同时约束两端。
- 后端无效任务引用为 unknown、无效清单项跳过；有效引用遇完整成功清单，匹配 present、未匹配 deleted。清单读取失败仍 unknown。
- 前端同时查 RepoTags 与 RepoDigests，只提供正向证据及当前清单大小回退；未匹配仍“镜像未核实”，服务端明确 deleted 的既有优先级不变。

## 验证

- 基线：新增 Go fake 清单用例得到 deleted；前端同例得到“镜像未核实”，均与预期存在不符。
- 共享表覆盖 Hub 别名双向矩阵、registry/端口、大小写、tag/digest差异及并存、空白、占位符、无效语法、算法/长度和路径长度边界。
- Go fake Agent 只覆写 ListDockerImages，其他方法嵌入 nil 接口，阻止真实访问；同表检验 tags/digests、unknown、完整清单未匹配、清单失败及非目标任务。
- TS composable 同表检验标签、digest、存在性文案及大小回退；纯 Go 定向测试、race、vet/build，前端定向与全量单元、lint、typecheck/build。验证仅限纯测试；真实浏览器回归待提交后的远端 CI。

### 本次结果

- 共享 fixture：86 例；前端 composable 132/132，全量单元 4112/4112（145 文件）。
- `go test -race ./internal/httpapi ./internal/docker -run 'DockerArtifact|ImagePullReceipt|ImagePullResultBinding' -count=1`、`go vet ./internal/httpapi`、`go build ./...` 通过。
- 前端 lint、typecheck、production build 与 bundle budget 通过。未运行 E2E、监听器或真实 Docker。
- 独立审查：406 组定向和 10000 组固定种子随机引用对照已安装官方解析包，无 Go/TS 差异。
- `distribution/reference v0.6.0` 从既有间接依赖标注为直接依赖，版本与 go.sum 均不变。
