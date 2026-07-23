# realtime-trend-visualization Specification

## Purpose
TBD - created by archiving change enhance-console-experience. Update Purpose after archive.
## Requirements
### Requirement: 会话内真实趋势
控制台 MUST 基于当前会话接收到的真实系统快照维护最多 60 个采样点，不得使用随机或固定演示数据。

#### Scenario: SSE 连续送达快照
- **WHEN** 系统完成至少两次成功刷新
- **THEN** 趋势数据按采集时间追加并保留最近 60 个采样点

### Requirement: 资源趋势图表
总览 MUST 展示 CPU、内存、系统负载和网络上下行速率图表，并提供当前值、单位、图例和交互 Tooltip。

#### Scenario: 用户查看资源趋势
- **WHEN** 趋势数据至少包含两个采样点
- **THEN** 图表绘制真实折线并在悬停或键盘聚焦时提供对应时间和数值

### Requirement: 图表降级与可访问摘要
图表 MUST 在数据不足、加载失败和小屏幕下保持可理解，并提供不依赖图形的文本摘要。

#### Scenario: 页面刚进入尚无历史
- **WHEN** 仅有零个或一个采样点
- **THEN** 图表区域显示正在积累实时样本和当前数值，而不是空坐标轴

