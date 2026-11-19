import { bindingsCall } from "@shared/lib/bindings";
import { SessionService } from "@bindings/reins/internal/session";
import type { SourceSelection } from "./source-app";
import type {
  SessionMessage,
  SessionPage,
  SessionEvent,
  SessionOverview,
  SessionRefreshResult,
  SourceApp,
  SourceStatus
} from "./types";

// 后端调用经 bindings service；类型与运行时同源（frontend/bindings/），
// Promise 收口断言见 @shared/lib/bindings。参数形状是 bindings 的逐位置
// 参数，Option 语义用 null 表达。

export function detectSources(): Promise<SourceStatus[]> {
  return bindingsCall(SessionService.DetectSources());
}

export function clearSessionCaches(): Promise<void> {
  return bindingsCall(SessionService.ClearSessionCaches());
}

type ListSessionsInput = {
  offset?: number;
  limit?: number;
  query?: string;
  reverse?: boolean;
  refresh?: boolean;
};

export function listSessions(
  sourceApp: SourceSelection,
  input: ListSessionsInput = {}
): Promise<SessionPage> {
  return bindingsCall(SessionService.ListSessions(
    sourceApp,
    input.offset ?? null,
    input.limit ?? null,
    input.query ?? null,
    input.reverse ?? null,
    input.refresh ?? null
  ));
}

export function refreshSessions(
  sourceApp: SourceSelection,
  input: Omit<ListSessionsInput, "refresh"> = {}
): Promise<SessionRefreshResult> {
  return bindingsCall(SessionService.RefreshSessions(
    sourceApp,
    input.offset ?? null,
    input.limit ?? null,
    input.query ?? null,
    input.reverse ?? null
  ));
}

export function getSessionOverview(
  sourceApp: SourceApp,
  sourceSessionId: string
): Promise<SessionOverview> {
  return bindingsCall(SessionService.GetSessionOverview(sourceApp, sourceSessionId));
}

// 详情正文全量返回:时间线分组器需要完整消息流,不再分页。
export function getSessionMessages(
  sourceApp: SourceApp,
  sourceSessionId: string
): Promise<SessionMessage[]> {
  return bindingsCall(SessionService.GetSessionMessages(sourceApp, sourceSessionId));
}

// 子代理弹窗取数：一次返回该 agent 的完整消息
export function getSessionAgentMessages(
  sourceApp: SourceApp,
  sourceSessionId: string,
  agentSessionId: string
): Promise<SessionMessage[]> {
  return bindingsCall(SessionService.GetSessionAgentMessages(
    sourceApp,
    sourceSessionId,
    agentSessionId
  ));
}

export function getSessionEvents(
  sourceApp: SourceApp,
  sourceSessionId: string
): Promise<SessionEvent[]> {
  return bindingsCall(SessionService.GetSessionEvents(sourceApp, sourceSessionId));
}
