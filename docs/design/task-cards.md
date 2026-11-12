# 任务卡：iOS 与 Web 的对比和统一设计稿

状态：草案（#5569）。只含对比、设计和待拍板项，**没有改任何卡片实现**；实现另开 issue。
对比基线：`origin/main` @ `d6b446fcea`（2026-11-12）。截图在 [`task-cards/`](task-cards/)，数据来自 `scripts/mock/mock_hub.py`
（`MOCK_TASKS=1 MOCK_TASKS_CUT=1 MOCK_USAGE_LIMIT=1 MOCK_TODO_ASKS=1 MOCK_TASK_DECISION=1`），iOS 是 iPhone 17 Pro 模拟器（iOS 26.0，英文），
Web 是本地 vite 页面，视口 430 px 宽（手机宽度，和 iPhone 可比），深浅色各一张。

## 0. 先更正：两端「任务卡」现在画在哪

上一轮对比把 `TaskCard`、`ReceiptCard` 当成聊天里的主要卡片。这一轮读调用点后发现并不是：

| 要看的东西 | Web 现在画在哪 | iOS 现在画在哪 |
| --- | --- | --- |
| 计划卡（待开始） | **Needs you 托盘**里的 `PlanCard`（`NeedsYouRow`）；聊天里 `simplified` 时 open 的计划不画 | Needs You 托盘（`AssistantActionBar`）和聊天（`FeedLine` → `PlanCard`） |
| 已开始的任务 / 计划 | 聊天里一张「交接卡」（`TaskHandoffs` / `PlanHandoffs`：箭头图标、标题、NEW、时间、状态，点开去 Agent 页） | 聊天里 `DispatchedTaskLinks`（`TaskOpenButton`，「Dispatched: 标题」） |
| 执行中进度 | **In progress 托盘**的行 | In Progress 托盘的行 |
| 结论（做完了 / 停了 / 失败） | 聊天里**不画**，只画「被模型服务打断」的那种（`interruptLook`）；其余靠交接卡的状态字 | 聊天里 `ReceiptCard`，四种：done / stopped / failed / interrupted |
| 要你拍板 | Needs you 托盘的 `TaskDecision`，早报卡的 `AcceptRow` | 托盘的 `BriefAcceptRow`（早报卡同一个组件） |

`web/src/views/assistant/Tasks.tsx` 里的 `TaskCard`、`OutcomeReceipt`、`TaskLine`，和 iOS `TaskViews.swift` 里的 `TaskCard`、`TaskLine`
在生产路径上没有调用点（Web 要 `FeedLineView` 不带 `simplified` 才走到，`ChatView.tsx:1780` 恒带；iOS 全仓没有 `TaskCard(` 的实例化），
只有测试在用。上一轮清单里「失败卡缺 Retry」「Web 的计划进度行 / 孤儿行」等条目比较的是这些**死代码**，本稿不再把它们当作差异；
是否清理见文末拍板点 P7。

下面逐项对比的是**线上真正可见的面**。

## 1. 已查实的上一轮「需要核对」

| 项 | 结果 |
| --- | --- |
| 目录 chip | 两端一致：都只显示目录最后一段（Web `cwd.split("/").at(-1)`，iOS `task.project`），截图里都是 `weather`（`web-plan-open-*`、`ios-plan-open-*`） |
| 权限 chip 是否常显 | **两端都常显**（截图里都是「Can edit files」）。上一轮「iOS 只在只读时显示」说错了；只读时两端都把它换成「Read-only」。不再是差异 |
| Web 失败文案 | 「Didn't start: <原因>」，红色，在交接卡下面（`web-handoff-failed-*`） |
| Web 绿 / 红 / 橙取值 | 见 §4 token 表。注意 Web 浅色的 `--red-tint` 是 `#f7f8f9`，几乎是中性灰，失败结论卡在浅色下看不出红 |
| iOS 有没有 Later / Dismiss | 任务拍板行没有：`apple/Persona/Views/Assistant/Tasks` 和 `TasksStore` 里没有对应的写接口调用；Web 的 `TaskDecision` 有 Later（只在本浏览器会话里推迟）和 Dismiss（落库，可撤销）。「Actions」菜单里的项我没有逐个展开看 |

## 2. 差异、取舍和理由

判断标准（顺序不代表优先级）：**单手**（拇指够得到、点击区够大、不用先展开再点）、**信息密度**、**两端一致**、**可发现性**（看得出能点、看得出下一步）。
不默认哪一端为准。

### D1 进度行：状态字的位置

- iOS：标题左，状态字**右对齐成一列**，灰色；第二行是在做什么。
- Web：状态字紧跟标题（同一行、小灰字），第二行是在做什么；行尾有 `›`，用量限制行尾是 ⓘ。
- **选 iOS 的右对齐状态列，加上 Web 的行尾 `›`。**
- 理由：状态是扫一眼要比较的东西，右对齐成一列比跟在不等长的标题后面好扫（密度、单手扫读）；iOS 的行没有任何「可点」提示，Web 的 `›` 让整行可点这件事一眼可见（可发现性）。Web 状态字跟标题走的写法在长标题被截断时会把状态挤没，右列不会。

### D2 状态词

- iOS：Starting / Working / Usage Limit / Unread / Needs You / Interrupted · N agents（首字母大写）。
- Web：Not started / Working / Usage limit / Unread（句首大写）。
- **选 iOS 的 `Starting`；大小写各用平台惯例（iOS Title Case，Web 句首大写），不强求一致。**
- 理由：这个状态是「马上要开始」，说 `Not started` 会让人以为卡住了；任务真的没起来是另一个状态（`Didn't start: <原因>`）。大小写是平台习惯（HIG 对按钮 / 标签用 Title Case），统一反而别扭。

### D3 用量限制的颜色和信息

- iOS：灰点 + 「Usage Limit」，没有第二行原因，没有重置时间。
- Web：橙色时钟 + 「Usage limit」，第二行「You've hit your usage limit.」，行尾橙色 ⓘ。
- **选 Web。** 用 `caution` 角色色（iOS `Theme.caution`，Web `--orange`）画时钟，第二行写原因和重置时间。
- 理由：用量限制是用户可以做点什么（等、换账号、继续）的状态，灰点读起来像「已结束」；原因和重置时间是用户决定要不要等的唯一依据。

### D4 Needs you 托盘：紧凑行 vs 内联展开

- iOS：托盘是一列**紧凑行**（标题 + 状态 + 一两行摘要），拍板 / 问题要点「Actions」或进详情才能答；托盘窗口很矮（约 280 pt），一屏看 2–3 行。
- Web：托盘里**直接展开成可答的卡**（计划批准、选项题、Bash 批准都在原地，按钮在卡底），一张卡就超过一屏。
- **不选其一，做成「折叠行 + 一键展开原地答」**：默认紧凑行（iOS 的密度），点行或行里的主按钮就在原地展开成 Web 那样的可答卡，答完收回。
- 理由：单手场景下，要答的东西一多，Web 的全展开会把后面的卡挤出屏、要一直滚；iOS 的紧凑行答一道题要多一次点击和一次页面跳转。折叠 + 原地展开两头都保住。这条有产品含义，见 P2。

### D5 计划卡（待开始）

两端结构几乎一样：小标题（图标 + 「Start like this」）→ 任务标题（多个任务带编号）→ chip 行（机器 / 目录 / 模型 / 权限）→ why → 「Start these directly」→ 按钮行。差异：

| 点 | iOS | Web | 选 |
| --- | --- | --- | --- |
| 按钮行位置 | 跟在内容后面，长了会滚出屏 | `sticky bottom-0`，卡比托盘高时按钮仍贴托盘底（#3822） | **Web**：单手最怕按钮被滚走 |
| 按钮宽度 | 三个等宽 | Start 撑满，另两个按内容 | **Web**：主操作大、次要操作小，拇指落点明确 |
| 「直接开始」 | 圆形勾选 +「Start These Directly」 | 方形 checkbox +「Start these right away from now on」 | **控件用各平台原生的；文案用 Web 的**：「from now on」说清了这是一条持续生效的偏好，不是只对这次 |
| 标题字号 | `bodyStrong`（17 semibold） | 14.5 px semibold | 各用自己的，见 §4（Web 整体字号比 iOS 小一档，是平台差异不是错误） |
| brief / 工作区 | 没有 | 卡里有 `task.brief` 折叠和工作区编辑 | **Web**：开始前能看到任务被什么标准验收，iOS 补 |

### D6 聊天里的已开始任务（交接卡）

- iOS：一个按钮样式的行，「Dispatched: <标题>」，多个任务时先写「Dispatched N」再列标题。
- Web：卡片：箭头图标、标题、NEW 标、时间、**状态**（Working / Unread…），右上 ↗；失败时下面一行红字原因。
- **选 Web 的卡片形态**（带状态和时间）。
- 理由：派出去之后用户最想知道的是「现在怎么样了」，iOS 这行只有标题，要点开才知道；Web 卡片在聊天里就能看到状态变化。（iOS 这部分我没能截到图，依据是源码，见 §6 局限。）

### D7 结论卡：画不画、画成什么样

- iOS：`ReceiptCard`：左图标（✓ 绿 / 时钟灰 / ✕ 红）+ 标题（「X is done / stopped, not done yet / didn't start / was interrupted」）+ 摘要最多 4 行（Markdown 链接）+ 「PR」「View」两个按钮；底色是状态色 12% 透明，stopped 用灰。
- Web（死代码 `OutcomeReceipt`）：同样结构，另有「Show all」展开、Copy / Quote、验收提示行；线上只画被打断的批次卡。
- **选：两端都画结论卡，结构取 iOS 的，加上 Web 的「Show all」和 Copy / Quote。** 对 Web 是恢复，对 iOS 是补两个小功能。
- 理由：结论里有 Agent 最后一段话和 PR，是任务的交付物；只靠交接卡的状态字，用户在聊天里看不到「做完了什么」，要去 Agent 页翻。摘要 4 行截断但无法展开是 iOS 现在的缺口。Web 是否有意去掉结论卡见 P3。

四种结论态的统一规格见 §3.3。

### D8 要你拍板：行内布局

- iOS：`BriefAcceptRow` 用 `ViewThatFits`：按钮放得下就一行，放不下就收成「Actions」菜单；文字占整行宽。
- Web：`AcceptRow` 文字在左、按钮在右同一行；430 px 宽下文字被挤成约 4 个字一行（`web-brief-to-verify-*`，「选缓存方案」那一条）。
- **选 iOS 的 `ViewThatFits`；窄屏下 Web 把按钮换到文字下面（堆叠），而不是收菜单。**
- 理由：文字被挤成一窄列是明显缺陷，可读性第一。收进菜单会让主操作（Accept / Take recommendation）多一次点击，单手场景下不如把按钮堆到下面、主按钮满宽。iOS 也建议同改：放不下时堆叠，而不是菜单。

### D9 要你拍板：Later / Dismiss

- iOS：没有。
- Web：Later（本会话推迟）+ Dismiss（落库、可撤销），吸底。
- **选 Web 的 Dismiss（落库、可撤销），Later 保留为「推迟」**；iOS 补。
- 理由：拍板项不想现在处理时，不能只有「答」一条路，否则它永远占着 Needs you 的计数（见 #3804）。可撤销的 Dismiss 比确认对话框轻。

### D10 颜色：失败和中断的底色

- iOS：失败 / 中断卡底色 = `danger` 12% 透明，深浅色都是红底。
- Web：`--red-tint`，浅色 `#f7f8f9`（几乎灰），深色 `#1f2022`（几乎黑）；红只在图标和文字上。
- **选 iOS：底色用 `danger` 的 12%。** Web 的 `--red-tint` 重新取值（浅色约 `#fbe9ea`，深色约 `#3a2224`，实现时再配对比度）。
- 理由：失败是要用户注意的状态，浅色下和普通灰卡片分不出来是缺陷；两端同一个语义色按同一个比例混合，也方便做 token 表。

### D11 圆角、间距、字号

- iOS 卡圆角 14、控件 10、气泡 20；Web 卡圆角 10、控件 8。
- **各平台保留自己的圆角**，但在 token 表里记成「同一个角色」，不要求数值相等。iOS 的大圆角是系统风格，Web 用 14 会在桌面密度下显得松。
- 内边距：两端卡片都是 16（iOS `Space.l`，Web `p-4`），gap 8 / 12 对应 `Space.s` / `Space.m`，已经一致。
- 字号：Web 写死的 `text-[14.5px] / [13px] / [12px] / [12.5px]` 要收成命名 token（§4），否则不能检查一致性；`12.5px` 这一档在 iOS 没有对应，并入 12 或 13。

## 3. 融合后的卡片结构

### 3.1 计划卡（待开始）

```
┌──────────────────────────────────────────┐
│ ☰ Start like this            (label,caption strong, secondary)
│ ① 任务标题                    (heading / 14.5 semibold)
│   [机器] [目录] [模型] [权限]  (chip 一行，可换行；只读时权限换成「Read-only」accent)
│   为什么这样安排              (caption, secondary)
│   ▸ 验收标准 brief            (折叠，Web 已有，iOS 补)
│ ② …（多任务：编号 + 分隔线）
│ ☐ Start these directly from now on
├──────────────────────────────────────────┤  ← 吸底
│ [   Start / Start N tasks   ] [Edit] [Don't start]
└──────────────────────────────────────────┘
```

字段顺序以 Web 为准（多出 brief 和工作区），按钮行吸底、主按钮撑满。已开始 / 已放弃后收成一行小字（「Started N tasks」/「Not started · N tasks」），失败的任务另起红色行。

### 3.2 执行中的进度行（托盘）和交接卡

```
进度行：  [状态标记]  标题                              状态字（右列）  ›
                      第二行：在做什么 / 原因 / 重置时间
交接卡：  [↳]  标题  [NEW]                                        ↗
          🕐 11 min · 状态字
          （失败：Didn't start: <原因>，红色）
```

| 状态 | 标记 | 状态字 / 颜色 | 第二行 |
| --- | --- | --- | --- |
| Working | 转圈（`ai`） | Working，secondary | 在做什么，一行；后台任务数 |
| Starting | 空心圈 + `caution` 点 | Starting，secondary | PR 号 / 「Starting the agent」 |
| Needs you | `caution` 实心点 | Needs you，`attention` / `cautionText` | 它在等什么（「Waiting for your answer」或问题本身）；行外圈 1.5 pt `danger` 描边仅在卡片形态下 |
| Usage limit | 时钟，`caution` | Usage limit，`caution` | 原因 + 重置时间；行尾 ⓘ |
| Interrupted | ⓘ，`danger` | Interrupted，`danger` | 打断原因 |
| Failed to start | ⓘ，`danger` | Didn't start，`danger` | 原因 |
| Unread / Done | ✓，`positive` / secondary | Unread / Done | 最后一句话（一行） |

### 3.3 结论卡（四态）

```
┌──────────────────────────────────────────┐
│ [图标]  标题（done / stopped / didn't start / interrupted）
│         摘要（Markdown，4 行；放不下出现「Show all」）
│         [PR #N]  [View]      （失败时：[Retry]；PR 在 App 内链接卡片里打开）
│         （复核中时：灰字一行「Waiting for review · N criteria」）
└──────────────────────────────────────────┘
```

| 态 | 图标 | 底色 | 按钮 |
| --- | --- | --- | --- |
| Done | ✓ 实心，`positive` | `positive` 12% | PR、View |
| Stopped（没做完） | 时钟，secondary | `Fill.well` / `--field` | PR、View |
| Failed to start | ✕ 实心，`danger` | `danger` 12% | Retry |
| Interrupted | ✕ 实心，`danger` | `danger` 12% | Continue、View |

### 3.4 要你拍板

```
标题                                  （secondaryMedium / 14 medium）
结论一句（verdict）                    （caption）
Recommended: …                         （caption secondary）
Waited 2 h · 来自 Agent「…」
窄屏：  [ Open ]  [ Later ]  [ Dismiss ]
        [      Take recommendation (满宽，主按钮)      ]
宽屏：  文字在左，按钮在右（和现在一致）
```

## 4. 双端共用 token 表

同一个角色，两端各用自己平台的值；表的目的是**每个角色只有一个名字**，实现时在 Web 的 `styles.css` 加 `--role-*` / `text-*` 别名，在 iOS 的 `Theme` 里已经有的直接用。

### 4.1 字号

| 角色 | iOS `Theme.Font` | iOS 默认 pt | Web 现状 | Web 建议类 |
| --- | --- | --- | --- | --- |
| 卡片标题 | `bodyStrong`（body semibold） | 17 | `text-[14.5px] font-semibold` | `text-card-title`（14.5） |
| 正文 / 摘要 | `body` / `secondary` | 17 / 15 | `--chat-body` 15 / `--chat-secondary` 14 | 沿用 |
| 行标题 | `secondaryMedium` | 15 medium | `text-[14.5px] font-semibold` | `text-row-title` |
| 元信息（时间、地点） | `caption` | 13 | `text-[12px]` / `[12.5px]` / `[13px]` 混用 | `text-meta`（13）；12 / 12.5 并入 |
| chip / 标签 / 计数 | `label` | 12 | `text-[12px]` | `text-label`（12） |
| 强调的小字（按钮式链接） | `captionStrong` | 13 semibold | `font-semibold text-[12.5px]` | `text-meta font-semibold` |
| 代码 / 路径 | `code` | 13 | `font-mono` | 沿用 |

### 4.2 间距

| 角色 | iOS `Theme.Space` | pt | Web |
| --- | --- | --- | --- |
| 紧凑 | `xs` / `sm` | 4 / 6 | `gap-1` / `gap-1.5` |
| 行内 | `s` | 8 | `gap-2` |
| 卡内分组 | `m` | 12 | `gap-3` |
| 卡内边距、页边 | `l` / `Space.page` | 16 | `p-4` / `px-4` |
| 区块之间 | `xl` | 24 | `gap-6` |

### 4.3 圆角

| 角色 | iOS `Theme.Radius` | Web |
| --- | --- | --- |
| badge / chip（胶囊） | `badge` 6 / capsule | `rounded-full` |
| 控件、行内小块 | `control` 10 | `--radius-control` 8 |
| 卡片 | `card` 14 | `--radius-card` 10 |
| 气泡 | `bubble` 20 | `rounded-[12px]`（用户气泡）、`[18px]`（Web 的路由气泡） |

### 4.4 角色色（浅色 / 深色）

| 角色 | iOS | iOS 值 | Web 变量 | Web 值 |
| --- | --- | --- | --- | --- |
| 强调（可点） | `Theme.accent` | 系统蓝 | `--accent` | `#0285ff` / `#3d9aff` |
| 成功 | `Theme.positive` | `#248A3D` / `#30D158` | `--green` | `#107638` / `#6dd697` |
| 危险 | `Theme.danger` | `#D93A3A` / `#FF5F57` | `--red` | `#bd3038` / `#ff9499` |
| 注意（点、图标） | `Theme.caution` | `#F08A00` / `#FF9F0A` | `--orange` | `#a64c00` / `#eea66d` |
| 注意（文字） | `Theme.cautionText` | `#B35400` / `#FF9F0A` | `--attention` | `#8f5c00` / `#f1b969` |
| AI / 工作中 | `Theme.ai` | `#5B4FD6` / `#9C94FF` | `--ai` | `#5b4fd6` / `#9c94ff`（**两端相同**） |
| 次要文字 | `.secondary` | 系统 | `--ink-2` | `#62656b` / `#a5a8ad` |
| 第三级文字 | `.tertiary` | 系统 | `--ink-3` | `#66696f` / `#979aa0` |
| 凹陷底（行、输入） | `Theme.Fill.well` | 系统 quaternary | `--field` | `#f2f2f3` / `#2b2c2f` |
| 成功底 | `positive` 12% | — | `--green-tint` | `#e8f5ed` / `#3dbb7224` |
| 危险底 | `danger` 12% | — | `--red-tint` | `#f7f8f9` / `#1f2022` → **要改，见 D10** |

两端的绿、红、橙数值并不相同（只有 AI 紫相同），这是各自为了对比度调的，不要求逐值一致；一致的是**角色**和**用在哪**。
Web 的 `--orange` 浅色 `#a64c00` 比 iOS 的 `caution` 暗很多，更接近 `cautionText`；Web 没有对应 `Theme.caution`（图标用的亮橙）的变量，建议补一个 `--caution`。

### 4.5 动画

沿用：iOS `Theme.Motion.state`（0.25 s）、`layout`（0.3 s）；Web 的 `duration-150/200` 和 `prefers-reduced-motion` 已经有 `motion-reduce`，不改。

## 5. 需要产品拍板的点

- **P1 拍板行：窄屏堆叠还是收菜单（D8）。** 推荐：堆叠，主按钮满宽。依据：菜单让主操作多一次点击。
- **P2 Needs you 托盘形态（D4）：紧凑行、内联展开、折叠行 + 原地展开。** 推荐：折叠行 + 原地展开。依据：单手滚动成本和答一道题的点击数两头都不吃亏。代价：要在 iOS 托盘里做可展开的行，工作量最大的一项。
- **P3 Web 是否恢复聊天里的结论卡（D7）。** 推荐：恢复，结构取 iOS，加「Show all」。依据：结论是交付物，状态字不替代它。若当时去掉是有意的（避免和交接卡重复），则改成「交接卡在做完后展开成结论」，同样要把摘要带出来。
- **P4 Dismiss / Later 的语义（D9）。** 推荐：Dismiss 落库可撤销，Later 只推迟当前会话；iOS 补。需要确认 iOS 的「Do not handle」（回复草稿用）和这里是不是同一个概念，要不要统一成一个词。
- **P5 失败底色（D10）。** 推荐：Web 的 `--red-tint` 重新取值。这是改已有界面的样子，按仓库约定属于「按用户明确要求改」，需要你确认才算。
- **P6 token 表是否进仓库作为双端约束。** 推荐：先放在文档里，Web 侧加 `scripts` 检查写死的 px 字号只能减少（仿 iOS 的 `TokenRatchetTests`），不立刻改页面。
- **P7 清理死代码。** Web 的 `TaskCard` / `OutcomeReceipt` / `TaskLine` 和 iOS 的 `TaskCard` / `TaskLine` 要不要删，还是 P3 恢复时复用。推荐：P3 定了再决定；定之前不删。

## 6. 局限

- **iOS 聊天里的交接卡和结论卡没有截到图**：在模拟器里主对话被一张 Bash 批准卡占满了聊天区（这个状态是 mock 里一个待批准的交互，不是真实使用的常态，我没有继续点掉它去看下面的聊天流）。这两项（D6、D7 的 iOS 一侧）依据源码（`DispatchedTaskLinks.swift`、`TaskViews.swift` 的 `ReceiptCard`），不是截图。
- Web 的结论卡（`OutcomeReceipt`）线上不画，所以也没有截图；§3.3 的样子是按源码和 iOS 的实际样子合成的设计，不是现状。
- 只看了英文；中文和日文的文案长度没有对比。
- 没有测对比度，D10 的建议色是估的，实现时要过 `StyleSpecTests` 同口径的检查。

## 截图索引

`task-cards/` 下，每项深浅色各一张（`-light` / `-dark`）：

- `ios-plan-open-*`、`web-plan-open-*`：计划卡（待开始）
- `ios-tray-progress-*`、`web-tray-progress-*`：In progress 托盘（working / starting / usage limit / unread）
- `ios-tray-needs-top-*`、`web-tray-needs-*`：Needs you 托盘
- `web-handoff-working-*`、`web-handoff-failed-*`：Web 聊天里的交接卡（运行中 / 没起来）
- `web-brief-to-verify-*`：早报里的拍板行（看 D8 的窄屏挤压）
