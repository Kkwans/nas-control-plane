# NCP 技能选型与使用边界（2026-10-13）

## 结论与项目依据

采用少量官方技能辅助安全审查、界面设计和浏览器复现；沿用仓库既有工程规范、组件库、图标与测试体系。技能不替代代码证据、真实运行结果或用户授权，热度不等于质量。

本次核对 `AGENTS.md`、`go.mod`、`frontend/package.json`：Go 1.26.4；Vue 3.5.40、TypeScript 5.9、Vite 8.1.5、Element Plus 2.14.3、Pinia 4.0.2、Vue Router 5.2.0；已有 `@lucide/vue` 1.27.0、Element Plus 图标、Playwright 1.62.0、axe 4.10.2、Vitest 4.1.10。

遵循项目定位：局域网内、登录后的 Root NAS 控制面；Agent 独占宿主机能力，浏览器只访问 Server。不得将正常 Root 管理能力本身误报为漏洞，也不得以通用安全建议擅自削弱产品能力。实际问题应落在身份认证、进程边界、输入验证、资源生命周期、秘密保护与操作结果一致性上。

## 已安装：仅当前开发工作区

已核实以下三个目录与 `SKILL.md` 存在于本次开发工作区的 `ncp-tools/skills` 中，与 `ncp` 仓库同级。它们不在用户个人 Codex 电脑上；没有修改用户全局配置，也不承诺后续会话自动加载。安装到目录不代表浏览器依赖已就绪或相关命令已运行。

| 技能 | 官方来源 | 在 NCP 中的使用范围 |
| --- | --- | --- |
| `security-best-practices` | [OpenAI 源文件](https://github.com/openai/skills/blob/main/skills/.curated/security-best-practices/SKILL.md) | 按 Go、Vue、通用 Web 前端参考逐项检查，输出带位置、可达性、影响与修复建议的发现 |
| `playwright` | [OpenAI 源文件](https://github.com/openai/skills/blob/main/skills/.curated/playwright/SKILL.md) | 按需浏览器快照、交互复现、截图和 trace；不是替换现有 E2E 配置的方案 |
| `frontend-design` | [Anthropic 源文件](https://github.com/anthropics/skills/blob/main/skills/frontend-design/SKILL.md) | 面板信息层级、设计 tokens、颜色、排版、状态反馈、响应式和视觉自评 |

### 已做内容检查及限制

已阅读三个本地 `SKILL.md` 全文、列出所含文件、静态阅读 Playwright 包装脚本，并核对 Go/Vue 安全参考的范围和开头约束。这是有限内容检查，不是对全部参考文本、依赖链或未来上游版本的完整安全认证。

- 安全技能含明确的 Go 和 Vue 参考，强调代码证据、禁止泄露秘密、不能通过关闭保护来修复问题。Go 参考标题基于 1.25.x，而项目是 1.26.4，涉及具体 API 或默认行为时应再对照当前 Go 文档。Vue 参考覆盖 Vue 3.x/Vite。TLS、Cookie、代理等问题需区分本地开发和实际部署条件。
- 设计技能是视觉与文案指导，目录中只有技能文本与许可证。其“大胆视觉方向”必须服从 NAS 控制台任务：优先可读性、密度、真实状态、错误恢复和危险操作确认；不为追求独特而引入装饰性首页、远程字体依赖或替换 Element Plus。
- Playwright 技能包含可执行包装脚本，并非纯文本包。脚本会调用 `npx --yes --package @playwright/cli playwright-cli`，可能下载未固定版本的软件。因此阅读或安装技能不等于授权运行该脚本；本次文档整理未执行它。后续若确需 CLI，应先检查可用工具、固定版本与依赖来源，并遵守当次授权。
- 技能文本、示例与外链均不能授予登录、上传、部署、修改 NAS、删除数据或安装社区代码的权限。源代码同步不能描述为已部署。

为识别本次读到的文本，记录 SHA-256；这些值不是上游提交签名或真实性证明：

| 本地文件 | SHA-256 |
| --- | --- |
| `security-best-practices/SKILL.md` | `7b3dae1ffc5434d890f3c65c8f552af52d0307fab3b35dec13013c9ca3844c4f` |
| `playwright/SKILL.md` | `0ffaabcc8e0990627c4725f18bf1c7955534a796c1c199e872909de2013ce6a8` |
| `frontend-design/SKILL.md` | `d91970639e9f5c37682ac7ab60094d35f1c7c1f38d731bd56396563aee10c1d3` |
| `playwright/scripts/playwright_cli.sh` | `aa3fdff5d0e4556177f4dfd5f04117e772aa54f94b6a2e34b6c0edf629c6b9b5` |

## 图标与验证：复用现有能力

图标使用已有 `@lucide/vue`，依据 [Lucide Vue 官方文档](https://lucide.dev/guide/vue/)统一尺寸、描边和语义映射；仅图标按钮应有可访问名称，危险操作同时有明确文字。保留 Element Plus 内置图标，不为统一外观进行无收益的全量替换，也不额外安装未经核实的“图标技能”。

浏览器回归优先仓库 `frontend` 的 `test:e2e` 脚本和现有 Playwright/axe 配置；单元验证沿用现有 Vitest。技能中的 CLI 交互流程只用于补充复现，不改变既有测试 runner。测试前应确认目标为获准的开发服务或 mock；不得把真实 NAS 的重启、删除、终端命令等操作当作普通浏览器冒烟测试执行。

继续按 `AGENTS.md` 执行项目最小验证要求：Go 编译、Vue 类型检查、生产构建、Compose 配置，以及获准部署后的健康检查。未部署时明确标注健康检查未执行。按改动风险追加针对性回归，不擅自强制 TDD，也不将全量测试变成项目当前阶段的新交付门槛。

## 已考察但未安装

| 候选与来源 | 可用价值 | 本项目限制 |
| --- | --- | --- |
| [Vercel web-design-guidelines](https://github.com/vercel-labs/agent-skills) | 键盘、焦点、表单、动效、暗色和可访问性复核 | 仅选框架无关规则；动态拉取规则应记录或固定版本，不引入 React/Next 专用建议 |
| [Anthony Fu vue](https://github.com/antfu/skills/blob/main/skills/vue/SKILL.md) 与 [vite](https://github.com/antfu/skills/blob/main/skills/vite/SKILL.md) | Vue 3.5、Composition API、TS 与 Vite 8 构建参考 | 个人维护的文档生成集合，README 标明实验性质；Vite 技能标记 2026-09-25、基于 8.3.1，需核对项目 8.1.5；不整包导入个人工具偏好 |
| [vuejs-ai/skills](https://github.com/vuejs-ai/skills) | Vue、Pinia、组件测试、调试专项，有评测流程 | README 明确是早期社区实验，并非 Vue 官方；Router 规则面向 v4，项目是 v5；Pinia 4 和 Vitest 4 兼容性需逐项确认 |
| [Microsoft playwright-cli](https://github.com/microsoft/playwright-cli/blob/main/skills/playwright-cli/SKILL.md) | 上游浏览器交互、trace、测试调试资料 | 与已安装 OpenAI Playwright 技能重叠，无需重复安装 |
| [OpenAI security-threat-model](https://github.com/openai/skills/blob/main/skills/.curated/security-threat-model/SKILL.md) | 基于仓库证据梳理信任边界、资产与滥用路径 | 仅在专门威胁建模任务中采用；需确认部署、暴露面和认证假设 |
| [Trail of Bits CodeQL](https://github.com/trailofbits/skills/blob/main/plugins/static-analysis/skills/codeql/SKILL.md) | Go、JS/TS 数据流静态分析 | 需先确认分析环境、依赖与适用范围；不能把扫描规则命中直接认定为可利用漏洞 |
| [obra/superpowers](https://github.com/obra/superpowers) | 调试和完成前验证方法，有行为评测基础设施 | 不安装整套流程或 hooks；其 TDD 强制流程不符合本仓库默认规范 |
| [UI UX Pro Max](https://github.com/nextlevelbuilder/ui-ux-pro-max-skill) | 配色、字体、图表及面板布局参考，支持 Vue | 社区候选，含脚本/CLI；本次未安装或运行，不将风格数据库当作设计质量保证 |

维护情况按实际可核对信息记录，不以网页被抓取日期代替最后提交日期。部分 GitHub 页面未展示最后提交日期，因此不能据此声称全部候选持续活跃。另已确认 [Vercel next-skills](https://github.com/vercel-labs/next-skills) 已迁移，且与本项目 Vue 技术栈不匹配，不纳入选型。

## 后续使用规则

1. 先查现有技能和依赖，按具体任务读取最小必要资料，避免重复安装与无关流程叠加。
2. 新引入候选前检查来源、许可证、版本、脚本、hooks、远程读取和权限范围；社区热度只作发现线索。
3. 与仓库规范冲突时遵守当前项目约束及用户授权；不自行启用 OpenSpec，不改变 Root 产品方向。
4. 使用真实代码、构建、针对性回归和截图判断收益；区分“已阅读”“已安装”“已运行”“已验证”“已部署”。
