# 会话统计升级为左栏顶层 tab

- 日期：2026-11-19
- 状态：生效中
- 来源：产品导航调整

## 结论

会话统计（usage 模式）从「会话列表的子页」升为左栏顶层 tab，与配置与分发/模型配置/历史会话
平级；历史会话的顶层入口保持不变，会话列表顶栏的「会话统计」按钮删除。名称统一为
「会话统计」：页面标题与左栏条目同名，不再出现按钮叫会话统计、页面标题叫用量统计的两套名字。

改动面：

1. **左栏**（`frontend/src/app/components/AppModeRail.vue`）：`MODE_COPY` 新增
   `{ id: "usage", label: "会话统计" }`（排在历史会话之后），图标沿用原顶栏入口的 lucide
   trending-up；删除 `activeMode` 的 usage→sessions 映射，选中态就是当前模式本身。
2. **外壳**（`frontend/src/App.vue`）：usage 进入顶层渲染链；摘掉 `open-usage` 与 `back`
   两条模式间跳转事件。KeepAlive 行为不变——会话工作区仍是唯一保留视图，离开 sessions
   时抓拍滚动位置的时机不变。
3. **会话列表**（`frontend/src/features/sessions/`）：顶栏「会话统计」按钮及其样式
   （`.sessions-stats-button`）与 `openUsageStats` 跳转一并删除。
4. **统计页**（`frontend/src/features/usage/UsageWorkspace.vue`）：不再带「← 返回」——
   顶层页换页由左栏承担，沿用「只有从别处钻进去的子页才带返回入口」的既有口径。
5. **连带清理**：`@shared/ui/BackLink.vue` + `back-link.css` 的唯一消费方是统计页返回入口，
   随顶层化删除；界面上「用量统计」的叫法统一改成「会话统计」（Go 侧 `usage_stats`、
   `usage-day-cache` 等后端概念名不变）。

## 关键取舍

- **usage 模式本身不用动**：`AppMode` 早有 `"usage"`，此前只是没有常驻条目；本次纯是导航
  归属变化，取数、筛选与缓存行为不变（统计页数据仍每次进入重新拉取）。
- **返回入口跟着页面层级走，不做兼容**：统计页成为顶层页后「← 返回」没有语义（没有来路
  可回），直接删除而不是留成摆设。

## 失效文档

- `2026-10-15-session-pages-navigation.md` 的「会话列表 ↔ 用量统计」节、usage 归并点亮
  `activeMode` 的说明与 `BackLink` 抽取说明失效，已在原文以删除线注记并指向本文。

## 证据

- `pnpm check` 全绿；`pnpm test` 28 个文件 258 个用例全部通过。
- 导航核对：左栏四个功能条目 + 底部「关于」各自正确点亮；会话列表 ↔ 会话统计来回切换，
  列表的搜索词、滚动位置经 KeepAlive 保留。
