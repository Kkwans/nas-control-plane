# 总览摘要可读性验收

结论：精确5ad0fc提交验收通过。此前390宽/30px字号“上次更新”时间右侧裁切已消失，十组真实PNG与文字行框JSON均验证完整。

## 证据
- SHA5ad0fc2838bda24eb9e82b0e0c07e5af665adcf3，Kkwans/nas-control-plane，dot/ncp-modernization。
- [CI38426367491](https://github.com/Kkwans/nas-control-plane/actions/runs/38426367491)，attempt1；前端job115396706275。57文件696单元、206/206浏览器（2.9分钟，含10新增可读性用例）通过；前端其他门禁、Go及Compose全部成功，release条件跳过。
- [Artifact11828772231](https://github.com/Kkwans/nas-control-plane/actions/runs/38426367491/artifacts/11828772231)：ncp-fixture-ui-5ad0fc2838bda24eb9e82b0e0c07e5af665adcf3-1。
- ZIP24,448,846bytes；SHA2566d774bfa69c296e869837ec40d44822fe73c5212ff3f5fbb5dfd6d465206402c，与GitHub一致。
- 290文件，总解压26,535,745bytes；安全路径/无符号链接及加密/格式/大小/数量/压缩比/CRC全部通过。截图摘要见文末。

## 实际像素与JSON
逐张查看320/390/768/1280/1440 ×15/30px根字号十张PNG；逐对验证JSON精确SHA、fixtureOnly、viewport尺寸，六个摘要字段contained=true、scrollWidth≤clientWidth+1，body不超viewport。

- 390/30px：三个指标自然纵向排列，完整“1/1”“1天0小时”“02:00:00”及对应标签，无末尾时间裁切。间隔清楚，没有互相压叠。
- 320/15px：前两指标一行，更新时间转第二行；320/30px三个指标纵向，完整时间不拆字。竖分隔线随原指标保留，视觉略不对称但没有错误列间距/重叠。
- 390/15px、768双字号：三指标清楚并排，文字不越界。
- 1280/1440双字号：横向摘要与状态标签保持原布局，没有异常空隙或列压缩；完整时间可读。
- 320/30px顶栏自然换行；页头说明在该极窄大字号下仍有既有省略号。此次目标为摘要数值/标签完整，不宣称整页所有长文案永不截断。

来源frontend/e2e/overview-readability.spec.ts + mockApi.ts；固定fixture时钟、HTTP截获、reduced-motion。15/30px为根字号，不是真实浏览器zoom；没有真实NAS/实时数据/部署验证。验收过程未修改仓库、未启动本地浏览器或服务。

## 已查看PNG摘要
- fixture-overview-summary-1280-font-15.png: d625908604467b0ee94c5240fd2428146865f8efe7f17c8723f8d735f19ff88f (172970bytes)
- fixture-overview-summary-1280-font-30.png: c05e9b977bcbcf2fea42495182fd9753cd909d766f665eb6234e8b55823140da (233841bytes)
- fixture-overview-summary-1440-font-15.png: 8b12f8bb18f05461d55f75c86ce8e38e091b0f3caa3850317659460dcfd99069 (184766bytes)
- fixture-overview-summary-1440-font-30.png: 3495243410f394c8e4df085a5b9302e019edc23f7d64f65329e2596f4dabcb04 (263435bytes)
- fixture-overview-summary-320-font-15.png: 19f2484976c0f113f1c97d33677f34536d4eacfdc689b2dcfd210579856bd832 (51800bytes)
- fixture-overview-summary-320-font-30.png: eaeccac411d677dbfb388fd9967d3447c726580a5a54f9d3a0262f64478a9440 (54124bytes)
- fixture-overview-summary-390-font-15.png: 3510680120c01fb1c1c33e4788afdcbf3918baa84b6ad421dee877218096d915 (55536bytes)
- fixture-overview-summary-390-font-30.png: eaf75d93fba4c8881aa8bd43396059359439bf486f20aa9a47c7f521febac71d (70684bytes)
- fixture-overview-summary-768-font-15.png: fbb33c24276ca4e0381b912f1523bb5fca56c3622e5ea33dbe383174b4bc04fa (111788bytes)
- fixture-overview-summary-768-font-30.png: 22b5a23e557f2e86a1d0b39187cd32af61577058618122bbd2d86d6b0c13c530 (145621bytes)
