# 最近任务中心 CI / 像素验收

结论：本次提交尚未通过验收。已实际检查 GitHub CI、下载校验 fixture artifact，并逐张查看全部 17 张现有相关 PNG。8 张 details PNG 因测试先失败而未生成，不能声称详情像素已验收。

## 可追溯证据
- 仓库：Kkwans/nas-control-plane，分支 dot/ncp-modernization。
- 提交：f06daee07e9f25d7fbdf41378edbad4d89c31d74。
- CI：[NCP CI #190，attempt 1](https://github.com/Kkwans/nas-control-plane/actions/runs/38417962795)，run ID 38417962795；前端 job 115367353522。
- [fixture artifact](https://github.com/Kkwans/nas-control-plane/actions/runs/38417962795/artifacts/11825755622)：ID 11825755622，ncp-fixture-ui-f06daee07e9f25d7fbdf41378edbad4d89c31d74-1。
- ZIP：19,644,644 bytes；SHA-256 db92509c5efb7b067c2c5b09492f93ef6ff73cfa34e8b527756c2262c4716fc9，与 GitHub 声明一致。
- 已下载并校验CI制品。解压前验证大小、摘要、路径无绝对路径/父目录穿越、无符号链接/加密文件、文件数/单文件/总解压大小/压缩比上限，全部 CRC 通过。232 文件，总解压 21,191,832 bytes；仅 PNG / capture-state JSON。

## 实际测试结果
前端 lint、format、typecheck、652 单元测试、web artifact regression、production build 成功；Go tests/vet/native 与 ARM64 build、Compose config 成功。release job 因开发分支而跳过。

浏览器总计 192：181 通过、11 失败，耗时 3.6 分钟。19 项新增 task-center 测试中 8 通过、11 失败，原有 173 项均通过。

阻断项：
1. task-center.spec.ts:129：取消后 Tab 循环时，dialog.contains(document.activeElement) 返回 false。模态焦点隔离验收失败。
2. :143：getByLabel('任务类型', exact:true).selectOption 找不到精确标签，60 秒超时。筛选交互尚未通过浏览器验收。
3. :192：clock.pauseAt(Date.now()) 报 Cannot fast-forward to the past；这是测试时钟竞争，当前测试不能证明隐藏/关闭轮询行为。
4. :354：四宽度 × 两字号全部报告 axe landmark-no-duplicate-banner（moderate），.el-drawer__header 与 .topbar 重复 banner。全部在 details 截图前停止。

8 项通过：总览入口、Docker 入口、保留 Docker 搜索、刷新失败禁用取消及恢复、无效记录错误态、390/768 移动导航互斥、旧镜像 unknown 产物语义。

## 实际像素与 capture-state 对照
已逐张查看 390、768、1280、1440 × 100%/200% 的 topbar 和 list PNG，以及旧镜像 unknown PNG，共17张。

- 所有顶栏任务/刷新按钮与健康信息无相互覆盖；capture-state 顶栏控件全部 unobscured=true，横向范围在 viewport 内，任务/刷新按钮至少44px。窄屏使用图标入口。
- 100% 列表：桌面/768 显示清晰的完成、失败、执行中语义；长中文 reference 在卡内折行。390 保持全宽抽屉，首屏可见第一条记录。固定视口以下出现底层页面，是 fullPage 截图格式所致，不应误解为抽屉在实际视口内漏底。
- 200% 列表：768/1280/1440 首屏保留完整工具栏及第一条任务。390×640 首屏只到筛选标签，任务在下方；源码有短高视口容器滚动，但此次 PNG 没有证明用户可完整滚动到任务与分页，须补充滚动/键盘可达截图与断言。关闭按钮在首屏可见。
- 明确证据质量问题：7/8 list JSON 在进入动画期间采样，drawer.x 等于 viewportWidth，记录为屏外；随后 screenshot(animations:'disabled') 的 PNG 却显示已打开。仅1440/100的 list 坐标已稳定。需在采样前等待抽屉开启动画结束，确保同一状态。
- 八组测试在 axe 之前已执行长标题/所有 dd 的文字范围、横向 overflow、reduced-motion、关闭按钮 in-viewport 检查；这些断言通过，但不能替代缺失的 details 像素检查。
- 旧镜像截图清楚显示 not-in-local-inventory:latest：已完成 + 镜像未核实，没有已删除，符合语义修复。
- topbar 200% 背景概览存在既有文字省略，390 概览时间末尾紧贴边界；此次任务入口本身无溢出。未将背景整页作为本片全量重设计验收。

## 边界
所有浏览器证据来自 frontend/e2e/task-center.spec.ts 与 mockApi.ts 的 HTTP fixtures。100%/200% 为15/30px根字号，不是真实浏览器 zoom；隐藏由 visibilitychange 模拟，不是操作系统后台标签验收。未启动本地浏览器/服务，未连接真实 NAS，未部署、未调用真实取消、未编辑仓库或推送。

当前修复应重新跑隔离 CI，并补齐全部 details 截图、稳定 capture-state、短屏滚动与焦点证据后再宣布完成。

## 已查看 PNG 摘要
- fixture-task-center-1280-font-100-list.png: 4c187fdb5121d32033ae585b4df726e7e753bd9fdaca43f1b056750207e979a8 (160248 bytes)
- fixture-task-center-1280-font-100-topbar.png: 1c1cbc5e1f111e8541d40067b2a89c41967d095b42ab29a3dc6d9bcda444aafe (178470 bytes)
- fixture-task-center-1280-font-200-list.png: 82e21d769883b0be73f26790f5825715aca197c60d9d5dfa38fe7d4ea7b65062 (259351 bytes)
- fixture-task-center-1280-font-200-topbar.png: 92d867c11ea6aba9a4fe356542fe679500ce2d166f9a3fb10bdf11bff86ea677 (271416 bytes)
- fixture-task-center-1440-font-100-list.png: 4298d09dd1cd18446d5abc5103bfa2dc31809e423d23541bd864d08c1c4e88e0 (173165 bytes)
- fixture-task-center-1440-font-100-topbar.png: b92959979a08bed764e7eca2fb9c9814b7cfa5031768c08ab383c66fac6e5b93 (190548 bytes)
- fixture-task-center-1440-font-200-list.png: 293fa4a7eebe06abe3df9d7c933df29a19afea34ec0817ad8be56376ed67f928 (280513 bytes)
- fixture-task-center-1440-font-200-topbar.png: fcad2014ad2c0f4fd62f58e78453f19791751534df692cef8653eff41078cebe (303702 bytes)
- fixture-task-center-390-font-100-list.png: 986d538ef3c18a5ce508c308944a07319cf3ce6ac8952dcff582b21a638370ba (115421 bytes)
- fixture-task-center-390-font-100-topbar.png: 2c9482fb4fb39c8eb03c5134519f6fadbe5569cada4d5d5c615ceb538bb7cf87 (121044 bytes)
- fixture-task-center-390-font-200-list.png: 6aba8e37cccd39e677aa464e1988c96fda8e002660d3e41457847a2b1481b26b (189828 bytes)
- fixture-task-center-390-font-200-topbar.png: f9d86bc2cd3494d541d184974bcde3551bdad3623d246fbcfbafc222d1d7ae6a (216770 bytes)
- fixture-task-center-768-font-100-list.png: 29e7a3a537a4ced8b37b2a9526d0d286c385f99631220cd795d7a66b374080ed (118434 bytes)
- fixture-task-center-768-font-100-topbar.png: 4d7f97c30ce66160163884fb0626cc56ed01a30e37baa7b139030371b1a588b0 (146750 bytes)
- fixture-task-center-768-font-200-list.png: 3f620f9fdc00d44790e31b4d1864fecad5874509e2129113c78eb54325282464 (205121 bytes)
- fixture-task-center-768-font-200-topbar.png: 23302ea6cdd310c9f8f344996bcf71dd6f32a568719d4dd304c0d21877a55911 (236015 bytes)
- fixture-legacy-image-unknown-artifact.png: 56d984625f11366443df88694223188264dc8cc7802196f1d44e8e8b23f1fd5f (125158 bytes)
