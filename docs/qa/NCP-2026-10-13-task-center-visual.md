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


# 复验：8b90897e（2026-10-13 15:23 UTC）

结论：仍未通过，失败由11项减少至3项；像素复验还发现通过用例中的同类关闭入口回归。保留以上首次失败记录，不视为最终关闭。

## 证据与结果
- SHA：8b90897e8626d76ec4738a555d3156222f22aa89，tree 9897ce09990f65a7600c85a524c11b855f27d5f5。
- [CI run 38420147521](https://github.com/Kkwans/nas-control-plane/actions/runs/38420147521)，attempt1；前端job115375012933。
- [Artifact11826517395](https://github.com/Kkwans/nas-control-plane/actions/runs/38420147521/artifacts/11826517395)：ncp-fixture-ui-8b90897e8626d76ec4738a555d3156222f22aa89-1。
- ZIP19,772,316bytes；SHA256 5a018f315c31645d5f8ff34f2ac0318de78a6977faa281fed0a94215c501441c，与GitHub一致。已下载并校验制品，246文件、21,367,320解压bytes；与首次相同的路径/符号链接/加密/大小/压缩比/CRC检查全部通过。
- 655单元及前端其他构建检查通过，Go/Compose成功；浏览器189通过、3失败（3.4分钟）；新增task-center16/19通过，原173项通过。
- 原先焦点、筛选标签、时钟用例通过。全部100%及390/200%通过包括axe；其余3组在axe之前失败，不能称其axe已完成。

## 剩余阻断
1. 768/1280/1440 ×200%根字号，在task-center.spec.ts:384关闭按钮toBeInViewport失败，ratio0；长详情展开后标题/关闭入口滚出可视区。
2. 实际像素发现768/1280/1440 ×100%详情截图也没有标题/关闭按钮。测试在最终scrollIntoView之前检查关闭按钮，因此仍通过。1440/100详情JSON：body.scrollTop319，关闭按钮top=-279.859、bottom=-235.859。必须在最后滚动后再次断言关闭按钮完整可见和中心命中；本项是实际产品问题，不能仅放宽断言。
3. 移动短屏sticky标题保留关闭入口，但顶部有20px滚动内容残留条，说明默认drawer body padding仍作用。390/200%初始body宽390，task-center x20宽350；是样式容器没有达到预期的佐证，非DOM scope属性直接证据。

## 逐张像素复验
实际查看24张相关PNG：8顶栏、8初始列表、5详情（四组100%+390/200%）、2张390滚动列表、1旧镜像截图。三个桌面200%详情PNG因失败未生成。
- 所有PNG尺寸现在等于viewport，避免首次fullPage伪边界；所有对应任务中心JSON的SHA/fixtureOnly/抽屉左右范围验证一致，首次7/8进场坐标不一致已消失。
- 顶栏所有宽度/字号仍可用，无任务按钮挤压刷新/状态区。
- 390滚动列表实际露出查看详情按钮，200%首屏任务在下方但可滚动访问；新增中心命中断言通过。长reference、中文错误、纯文本script字符串、错误代码折行可读。固定标题盖住上方已滚走的部分内容属于滚动裁切，顶部20px残留条仍待修正。
- 桌面100%详情完整字段和错误内容在卡内折行，关闭入口丢失为主要阻断。
- 旧镜像仍为已完成+镜像未核实。
- 390/200%背景概览时间右侧裁切仍可见，属于原背景页面字号压力问题，不作为任务中心关闭回归的替代结论。

下一次验收需要所有宽度固定标题/正确body样式，所有滚动后关闭按钮可见/命中，完整8张详情PNG。无真实NAS验证、无部署、无仓库改动。

## 本次已查看PNG摘要
- fixture-task-center-1280-font-100-details.png: dbdfa21c0ee26f14a8b0676a0cd5325ae7ce09e16f3d4293cfde85de33ee9892 (257928 bytes)
- fixture-task-center-1280-font-100-list.png: e241e2db9503da03ecf5676bb00cd1c9d6bfc86b815f9a9411c8e52d0459f7ff (158931 bytes)
- fixture-task-center-1280-font-100-topbar.png: d625908604467b0ee94c5240fd2428146865f8efe7f17c8723f8d735f19ff88f (172970 bytes)
- fixture-task-center-1280-font-200-list.png: d973c87434796ed94f5de8ef15f87cc272c43ef31f500488957c91101ab34d4d (223437 bytes)
- fixture-task-center-1280-font-200-topbar.png: c05e9b977bcbcf2fea42495182fd9753cd909d766f665eb6234e8b55823140da (233841 bytes)
- fixture-task-center-1440-font-100-details.png: e4322f077b918d0df7b1c18ae3181ca51c94630e7eaa148e5c061adb0f114eff (272895 bytes)
- fixture-task-center-1440-font-100-list.png: 9ee599c059611a8587c323c085fffa246008147e746c5fa371ec21281aedff1f (171644 bytes)
- fixture-task-center-1440-font-100-topbar.png: 8b12f8bb18f05461d55f75c86ce8e38e091b0f3caa3850317659460dcfd99069 (184766 bytes)
- fixture-task-center-1440-font-200-list.png: b2db1c3d02736e9319ee6bf03a4df553cfa2674e1134cc92fcbe247b6b25bc1c (242602 bytes)
- fixture-task-center-1440-font-200-topbar.png: 3495243410f394c8e4df085a5b9302e019edc23f7d64f65329e2596f4dabcb04 (263435 bytes)
- fixture-task-center-390-font-100-details.png: 49919875ec7f3a06ed3ee7ff86d358f4650a88e31704115cb164cb9b10c12365 (75521 bytes)
- fixture-task-center-390-font-100-list.png: f334084df4bc9830b4d8b0d891b931e4c7eeb41debdfce7b10ac4bffd0fb2a2c (49932 bytes)
- fixture-task-center-390-font-100-scrolled-list.png: 5f96cd2d2b76d20346901f678e5c5803f96784966680b13b3499f66bd3d1a286 (39983 bytes)
- fixture-task-center-390-font-100-topbar.png: 3510680120c01fb1c1c33e4788afdcbf3918baa84b6ad421dee877218096d915 (55536 bytes)
- fixture-task-center-390-font-200-details.png: 3e16bb41058fa7a65e57599c1dcbff39ad7fc8c50a272c719d442ad80e2e790d (51908 bytes)
- fixture-task-center-390-font-200-list.png: e98aea639aa172066943f3b20043ada9f9a4b1b649591e4fadd8206863e2668a (56068 bytes)
- fixture-task-center-390-font-200-scrolled-list.png: baefcbcef7587f70e13f5b900a56a94e80e917760e906532ad2115ff11986061 (58849 bytes)
- fixture-task-center-390-font-200-topbar.png: 577874f43e50e1e1c0c8f4120ba7131f3a291e8aeaeed4e3314f1e60e9024ce0 (80439 bytes)
- fixture-task-center-768-font-100-details.png: 078e1f5fc7e77dc80988df281d94dc18747fdbdf04cbeda1995e0c717d21571f (180551 bytes)
- fixture-task-center-768-font-100-list.png: 7639e9860b3c3e36d3945b952693942fcbbe4176916c83dfaffc8b266d6feb71 (86659 bytes)
- fixture-task-center-768-font-100-topbar.png: fbb33c24276ca4e0381b912f1523bb5fca56c3622e5ea33dbe383174b4bc04fa (111788 bytes)
- fixture-task-center-768-font-200-list.png: aac570a3d581be4ed050dc7f8e980d1e82ef687f0b5aa853ba1b5df80f443aaa (116190 bytes)
- fixture-task-center-768-font-200-topbar.png: 22b5a23e557f2e86a1d0b39187cd32af61577058618122bbd2d86d6b0c13c530 (145621 bytes)
- fixture-legacy-image-unknown-artifact.png: 56d984625f11366443df88694223188264dc8cc7802196f1d44e8e8b23f1fd5f (125158 bytes)


## c5938af最终验收（2026-10-13 15:47 UTC）

- 精确提交c5938afc673999771fc3b86abac535cfe4a1d072，[完整CI](https://github.com/Kkwans/nas-control-plane/actions/runs/38423448527)通过：655项单元、192项浏览器，含全部19项任务中心用例。Go/Compose通过，发布跳过。
- [制品11827507615](https://github.com/Kkwans/nas-control-plane/actions/runs/38423448527/artifacts/11827507615)为21,355,559字节，SHA256 aa96c70e8e36b8753c7aa00f77120f99998e21b141b4f05cc7b154efb8d7ff13；264文件、23,318,071字节解压内容，路径/CRC/大小等检查通过。
- 实际查看全部33张相关PNG：四宽度390/768/1280/1440 × 根字号100/200%，每组顶栏/列表/滚动列表/详情四态，加旧镜像页一张。截图与声明视口一致；33组精确SHA与fixture标记正确。
- 八组最终滚动后关闭按钮均为44×44、完整可见且中心命中；标题sticky top0，抽屉body padding0。八组查看详情按钮也完整可见且中心命中。此前390/100%用例现按钮top506.234375、bottom550.234375，视口640，未降低ratio1要求。
- 全部组合无横向overflow，长引用、中文错误与代码正常折行，script字样作为文本；小屏整体滚动可达列表/详情/分页。旧镜像页显示“已完成 / 镜像未核实”，不误报删除。axe、reduced-motion、键盘焦点和生命周期fixture通过。
- Task-center范围无剩余验收阻断，保留11→3→1失败历史。390/200%背景总览时间既有裁切不属于本片，未宣称整页完美。测试为Chromium加模拟API；根字号15/30px不等同浏览器zoom，也不代表真实NAS任务、手机软键盘、OS后台标签或人工读屏验收。
