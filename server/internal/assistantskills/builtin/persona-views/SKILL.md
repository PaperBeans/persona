---
name: persona-views
description: Use before constructing persona-view cards, live views, named detail pages, forms, dashboards, or free-form HTML views.
---

# Building Persona views

Read this guide before constructing a `persona-view` code block or free-form HTML view. Keep conclusions and key numbers in the reply. Every card needs a plain-text `fallback`; cards never replace `ask_user` or tool approval. If the platform does not declare HTML support, use supported components and a fallback instead.

## Components, limits, and actions

- 需要用户比较、选择、填条件或看进度时，主动选合适的卡片；一句话说得清就不画。结论和关键数字仍写在正文，每张必须有纯文本 `fallback`（精确字段名，最多 2000 字）。
  用 `persona-view` 代码块，内容与扩展共用同一个 View schema；单组件简写也必须带 fallback。每块最多 64 KB，一次回答最多 3 张，手机一屏能看懂，细节放命名详情页或正文。
- 选择用带 reply 动作的 row／button，比较用 table，关键数用 metric／stat_grid，趋势用 chart，过程用 progress 活卡片，条件用 field 表单。
  progress 必填 label 和 ratio（0 到 1 的数字，如 0.5），说明文字放 detail，没有 value 字段；metric 必填 label 和 value；stat_grid 把多项放进 stats 数组，每项也是 label 和 value；metric／stat_grid 的 value 是写好的文字；chart 的 value 是有限数字，kind 是 line／bar，叠加用 bar 加 stacked=true。最多 4 个 series，写明 name 并保留明细表，不只靠颜色区分。
  markdown 放说明，empty／loading 放空态／等待状态。只用已支持的组件，不要编新 type。
- 动作 id 在整张卡片及其详情页里唯一，每个动作只选 reply／open／push.page／pop 之一。reply 发回本对话；submit=true 配 reply，提交当前页声明的字段，不能收密码、token 或 secret。
  一张卡片只提交一次，更新不会清除已选结果；要新一轮选择就建新卡片。confirm 是用户看到的原生二次确认，但卡片不能代替 ask_user 或工具审批。
  要等用户答案才能继续的决定用 ask_user；卡片中的可选建议发出后结束这一轮，收到回复再继续，不猜用户选了什么。

## Complete valid example

选择和填条件（number／choice／toggle 的值都用字符串）：

```persona-view
{"title":"选择方案","fallback":"方案 A 更省，方案 B 更快。选一个，或填预算后继续。","sections":[{"items":[{"type":"row","title":"方案 A","subtitle":"更省","action":{"id":"a","reply":"选择方案 A"}},{"type":"button","title":"看对比","action":{"id":"details","push":{"page":"details"}}},{"type":"field","name":"budget","label":"预算","kind":"number","value":"100"},{"type":"button","title":"按预算继续","action":{"id":"submit","reply":"按这个预算继续","submit":true,"confirm":"确认使用这个预算？"}}]}],"pages":{"details":{"sections":[{"items":[{"type":"table","columns":[{"title":"方案"},{"title":"特点"}],"rows":[["A","更省"],["B","更快"]]},{"type":"button","title":"返回","action":{"id":"back","pop":true}}]}]}}}
```

- open 只用 https 或已有的 Persona 对话／任务／文件／知识库链接，沿用系统给的链接，不自己拼个人地址。链接在 App 内打开，不能要求退到外部浏览器。
  push.page 指向根 View 的 pages，最多 8 个命名页、4 层导航，页内不再嵌套 pages；pop 返回上一页。字段 name 和组件 key 在各自页内唯一，更新时保留稳定 key。
- 本对话里会继续变化的进度用活卡片：view_show 后记住 id，用 view_update 替换同一张（换 ratio、detail 和 fallback），不新增消息，也不重开已答卡片；view_get 看 version 和 actions，冲突先读再改。只改本对话的卡片，提醒用户用 notify_user。
  用户会反复回来看的追踪（打卡、长期关注）在有 `page` 工具时做成仪表盘。

## Free-form HTML

- HTML 自由绘制只在平台明确声明支持时使用；未支持时用现有组件和 fallback，不假定桥接可用。支持时默认进入可见区域自动运行，离开销毁；页面必须自包含、离线，不能读 App 的 token／cookie／文件。
  persona.reply／persona.open 都须原生确认，reply 仍只能提交一次，不收密码。不要把普通 iframe 或 ready 超时说成完整的网络／执行隔离。
  这样写的页面在所有端都能跑：用元素、class、内联和 <style> 样式、SVG、事件和 2D canvas（形状、文字、渐变）来画；排版交给 CSS（flex／grid／百分比），
  脚本只改内容和样式，不读回元素尺寸（getBoundingClientRect、offsetWidth 之类）；图片用 data: 内联的 <img>，canvas 不画图片（不用 drawImage）；链接交给 persona.open。
- 工具返回的字段错误或 Stop 校验反馈要在当前轮修正，保留已完成的工作，不重复执行操作。修不成有效卡片就用纯文本说明，不能把格式错误当成用户的新请求。
