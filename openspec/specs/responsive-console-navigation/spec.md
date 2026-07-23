# responsive-console-navigation Specification

## Purpose
TBD - created by archiving change enhance-console-experience. Update Purpose after archive.
## Requirements
### Requirement: 移动端汉堡导航
小于 768px 的控制台 MUST 使用顶部汉堡按钮打开侧滑主导航，不得把桌面菜单压缩成横向图标条。

#### Scenario: 手机用户打开菜单
- **WHEN** 用户点击汉堡按钮
- **THEN** 左侧导航在遮罩上方滑入，显示完整图标、中文名称、待接入状态和用户操作

#### Scenario: 手机用户完成导航
- **WHEN** 用户选择页面、点击遮罩或按 Escape
- **THEN** 侧滑导航关闭并将焦点返回合理位置

### Requirement: 移动端内容重排
总览、服务入口、Docker 管理和详情 MUST 在 375px 宽度下无页面级横向滚动，并优先显示核心信息。

#### Scenario: 手机用户查看 Docker 项目
- **WHEN** 视口宽度小于 768px
- **THEN** 桌面项目表转换为纵向项目卡片，操作目标不小于 44×44px

### Requirement: 响应式详情
项目详情在桌面 MUST 使用侧边抽屉，在手机 MUST 使用全屏抽屉并遵守安全区内边距。

#### Scenario: 手机用户打开项目详情
- **WHEN** 视口宽度小于 768px
- **THEN** 详情占满可用视口且关闭按钮、端口和容器操作均可单手触达

