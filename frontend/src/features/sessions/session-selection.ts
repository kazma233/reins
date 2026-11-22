import { isSourceApp, type SourceSelection } from "./source-app";
import { sessionIdentityKey } from "./session-identity";
import type { SessionOverview, SessionSummary } from "./types";

// 详情选中态的解析规则（列表页点开 → 详情加载 → 返回保留的整条链路共用）：
// 组件只做编排，回退顺序与匹配口径只此一份。

// 选中摘要的两级回退：先在当前可见列表里按会话身份找（合并视图下复合键
// 自带来源，不会误伤）；找不到再退到详情里保留的摘要（返回列表后列表重拉
// 的窗口期，详情仍是刚看过的那条）。两级都不中返回 null。
export function resolveSelectedSummary(
  sessions: SessionSummary[],
  overview: SessionOverview | null,
  selectedKey: string | null
): SessionSummary | null {
  if (selectedKey === null) {
    return null;
  }

  const visibleSummary = sessions.find(
    (session) =>
      sessionIdentityKey(session.sourceApp, session.sourceSessionId) === selectedKey
  );
  if (visibleSummary) {
    return visibleSummary;
  }

  const retainedSummary = overview?.summary ?? null;
  const retainedMatches =
    retainedSummary !== null &&
    sessionIdentityKey(
      retainedSummary.sourceApp,
      retainedSummary.sourceSessionId
    ) === selectedKey;
  return retainedMatches ? retainedSummary : null;
}

// 详情是否可加载：合并视图（"all"）下详情按会话自身的来源加载，只有单一
// 来源才要求来源一致。类型收窄谓词——通过即 summary 非 null。
export function canLoadDetail(
  summary: SessionSummary | null,
  selectedSource: SourceSelection
): summary is SessionSummary {
  if (summary === null) {
    return false;
  }
  if (isSourceApp(selectedSource)) {
    return summary.sourceApp === selectedSource;
  }
  return true;
}

// 详情加载期间的占位骨架：先给出摘要与转录路径让详情页立起骨架，计数与
// 子代理名册等真值由随后到达的 overview 填充。
export function placeholderOverview(summary: SessionSummary): SessionOverview {
  return {
    summary,
    sourcePaths: [summary.transcriptPath],
    messageCount: null,
    eventCount: null,
    agents: []
  };
}
