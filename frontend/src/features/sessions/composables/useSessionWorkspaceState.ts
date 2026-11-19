import { ref } from "vue";
import { sessionIdentityKey } from "../session-identity";
import { ALL_SOURCES, type SourceSelection } from "../source-app";
import type { SessionOverview, SessionSummary, SourceStatus } from "../types";

// 会话工作区的数据状态工厂:由 SessionWorkspace 调用一次,状态随组件实例
// 存活——KeepAlive 保留组件实例,列表数据、选中与筛选因此跨模式保留。
export function useSessionWorkspaceState() {
  const sources = ref<SourceStatus[]>([]);
  // 默认展示跨来源合并视图。
  const selectedSource = ref<SourceSelection>(ALL_SOURCES);
  const sessions = ref<SessionSummary[]>([]);
  const selectedSessionKey = ref<string | null>(null);
  const sessionOverview = ref<SessionOverview | null>(null);
  const loadingSources = ref(false);
  const loadingSessions = ref(false);
  const loadingDetail = ref(false);

  function setSources(nextSources: SourceStatus[]) {
    sources.value = nextSources;
  }

  function setSelectedSource(source: SourceSelection) {
    if (selectedSource.value === source) {
      return;
    }

    // Switching source invalidates the current list and detail.
    selectedSource.value = source;
    sessions.value = [];
    selectedSessionKey.value = null;
    sessionOverview.value = null;
  }

  function setSessions(nextSessions: SessionSummary[]) {
    sessions.value = nextSessions;
  }

  function appendSessions(incomingSessions: SessionSummary[]) {
    // Dedup by session identity so paginated loads stay stable.
    const sessionsById = new Map(
      sessions.value.map((session) => [
        sessionIdentityKey(session.sourceApp, session.sourceSessionId),
        session
      ])
    );

    for (const session of incomingSessions) {
      sessionsById.set(
        sessionIdentityKey(session.sourceApp, session.sourceSessionId),
        session
      );
    }

    sessions.value = Array.from(sessionsById.values());
  }

  function setSelectedSessionKey(sessionKey: string | null) {
    selectedSessionKey.value = sessionKey;
  }

  function setSessionOverview(overview: SessionOverview | null) {
    sessionOverview.value = overview;
  }

  function setLoadingSources(value: boolean) {
    loadingSources.value = value;
  }

  function setLoadingSessions(value: boolean) {
    loadingSessions.value = value;
  }

  function setLoadingDetail(value: boolean) {
    loadingDetail.value = value;
  }

  return {
    sources,
    selectedSource,
    sessions,
    selectedSessionKey,
    sessionOverview,
    loadingSources,
    loadingSessions,
    loadingDetail,
    setSources,
    setSelectedSource,
    setSessions,
    appendSessions,
    setSelectedSessionKey,
    setSessionOverview,
    setLoadingSources,
    setLoadingSessions,
    setLoadingDetail
  };
}
