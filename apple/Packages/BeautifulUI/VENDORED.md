# BeautifulUI（拷入的副本）

来源：BeautifulUI 仓库的 `d22da54`（`persona-full-refactor` 分支，在 `persona-host-hooks` 的 `02907da` 之后：胶囊放不下时可以截断（`BUIPill(truncates:)`）、单行的任务行、状态胶囊、筛选 chip、分段切换、工具组表头和改动文件 chip、审批卡和上下文卡片的宿主选项、列表行和分组、空状态、读数卡、侧边栏的行和搜索框；iOS 上转圈和扫光用 Core Animation），`BeautifulUI/` 目录原样拷入，
作为本地 Swift Package 由 `apple/project.yml` 引用。许可：`LICENSE`、`LICENSE-beautifului`、`NOTICE`，根目录的
`THIRD_PARTY_NOTICES.md` 也有一份。

**不在这里改库的代码**：要改先改 BeautifulUI 仓库，再整个目录重新拷一次：

```sh
rm -r apple/Packages/BeautifulUI   # 先看一眼 git status，确认这里没有本地改动
git -C <BeautifulUI 仓库> archive --format=tar <提交> BeautifulUI | tar -x -C apple/Packages/
```

然后把上面的来源提交改掉。Persona 用到的宿主接口（`typeScale`、`entrances`、`ToolChipRow`、`ToolChipGroup`……）见库的 README
「给宿主 App 的接口」。

## 本地补丁（还没进 BeautifulUI 仓库）

下次重新拷入前，先把这些补进 BeautifulUI 仓库，否则会丢：

- `TaskRow.titleLeading { … }`（#3004）：标题第一行、标题文字前面的视图（Agents 行的「终端」/「飞书群」来源标签）。
  leading 槽固定 24pt 宽，只放状态徽标；之前把标签也塞进去，居中后两边溢出，徽标被挤出卡片、标签压住标题。
- `ToolChipLabel`'s label (#2986): `.layoutPriority(1)` in place of `.fixedSize()`. Whole when it fits, as before; an MCP
  tool's long "Server · Tool" label ends in "…" instead of pushing the row past the screen's edge.
- `ToolChipGroup.Chip.truncatesTail` (#2986): a chip whose detail is words ("GitHub · Create issue CSV export times out…")
  gives way at its end; without it, the middle truncation cut into the verb. Defaults to `false`, the old middle truncation.
