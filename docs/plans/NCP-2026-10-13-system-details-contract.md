# 系统详情响应边界

日期：2026-10-13。范围仅 GET /system/details 的前端解码和相关 OpenAPI，保留 Root Agent → Server 能力边界。

## 已证实问题

原判定只检查数组容器，storage.mounts=[null] 可以通过，随后页面读取 item.path 崩溃；同类风险覆盖网卡地址、RAID设备、监听端口元信息以及可选能力。修复应在数据进入页面前拒绝整体无效响应，不能过滤坏条目后宣称空列表成功。

## 实现与兼容

- 新 parseSystemDetailsResponse 按声明逐层检查记录、字符串、布尔、有限数值、枚举与数组元素；错误继续使用 SYSTEM_DETAILS_RESPONSE_INVALID，不额外请求、记录或回传输入内容。
- 保留服务端合法的空字符串、0、false、负温度、网速 -1、空磁盘 kind/role 与可选字段缺省；容量接受超过 JS 安全整数范围的现有 JSON 数值，不承诺由此恢复已丢失精度。
- 只对明确的嵌套 Go nil slice 转换 null 为新空数组；顶层必需数组、错误对象、[null]、缺必需字段仍失败。返回正确类型的新结构，不更改输入；未知扩展字段保留。
- OpenAPI 补全相关必需字段及上述兼容值。详情专用能力/警告 schema 避免改变其他接口；共享 ProbeWarning 不变。
- requestSystemDetails 保留既有 fetch 注入、URL、同源凭据与 AbortSignal，不重放请求。

## 验证与边界

主线程独立完整前端63文件1059项单元、全量lint、类型检查、生产构建和540KiB预算通过。71 项解码回归与独立审查通过，覆盖各嵌套坏类型、零值、缺省、嵌套 null、输入不变及超安全整数容量。四项纯 fixture 浏览器用例覆盖390/1440初始错误和重试、畸形刷新保留旧设备/存储、合法零值跨分类显示。静态发现/lint/严格TypeScript通过；实际浏览器及截图待CI。测试拦截全部API，Mihomo检查返回受控fixture，其他写入被阻止；不连接NAS或改网络设置。

DNS辅助读取仍与详情一同等待，同页DNS编辑版本竞争仍未处理；此切片不声称全部系统API均已完整校验。
