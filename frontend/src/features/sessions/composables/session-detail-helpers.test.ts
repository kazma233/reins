import { describe, expect, it } from "vitest";
import {
  agentDisplayLabel,
  isSubagentLifecycleEvent,
  prepareTimelineEvent,
  sessionIdFromPayload
} from "./session-detail-helpers";
import type { SessionAgent, SessionEvent } from "../types";

function event(overrides: Partial<SessionEvent> = {}): SessionEvent {
  return {
    id: "evt-1",
    kind: "tool_run",
    timestamp: 1,
    summary: "ran a tool",
    payload: null,
    sessionId: null,
    ...overrides
  };
}

function agent(sessionId: string, label: string): SessionAgent {
  return { sessionId, label, isRoot: false };
}

describe("prepareTimelineEvent", () => {
  it("derives the stable key from the agent session id, falling back to root", () => {
    expect(prepareTimelineEvent(event()).key).toBe("root:evt-1");
    expect(
      prepareTimelineEvent(event({ sessionId: "child-1" })).key
    ).toBe("child-1:evt-1");
  });

  it("reads the agent session id from the payload when the column is empty", () => {
    const prepared = prepareTimelineEvent(
      event({ payload: { session_id: "from-payload" } })
    );
    expect(prepared.agentSessionId).toBe("from-payload");
  });

  it("flags exactly the subagent lifecycle kinds and composes search text", () => {
    const lifecycle = prepareTimelineEvent(event({ kind: "subagent_started" }));
    expect(lifecycle.isSubagentLifecycle).toBe(true);
    expect(lifecycle.searchText).toContain("subagent_started");
    expect(lifecycle.searchText).toContain("ran a tool");
    expect(prepareTimelineEvent(event({ kind: "tool_run" })).isSubagentLifecycle).toBe(false);
  });
});

describe("isSubagentLifecycleEvent", () => {
  it("accepts the closed set of lifecycle kinds only", () => {
    for (const kind of [
      "subagent_started",
      "subagent_spawned",
      "subagent_closed",
      "subagent_notification"
    ]) {
      expect(isSubagentLifecycleEvent(event({ kind }))).toBe(true);
    }
    expect(isSubagentLifecycleEvent(event({ kind: "message" }))).toBe(false);
  });
});

describe("sessionIdFromPayload", () => {
  it("extracts a string session_id and rejects everything else", () => {
    expect(sessionIdFromPayload({ session_id: "s1" })).toBe("s1");
    expect(sessionIdFromPayload({ session_id: 42 })).toBeNull();
    expect(sessionIdFromPayload({ other: "s1" })).toBeNull();
    expect(sessionIdFromPayload(null)).toBeNull();
  });
});

describe("agentDisplayLabel", () => {
  it("keeps primary and fallback labels untouched", () => {
    const agents = [agent("root", "主 Agent")];
    expect(agentDisplayLabel("root", agents)).toBe("主 Agent");
    expect(agentDisplayLabel("unknown", [])).toBe("Agent unknown");
  });

  it("disambiguates duplicate labels with the session id prefix", () => {
    const agents = [agent("aaa1", "Explore"), agent("bbb2", "Explore")];
    expect(agentDisplayLabel("aaa1", agents)).toBe("Explore · aaa1");
  });

  it("leaves unique named labels alone", () => {
    const agents = [agent("aaa1", "Explore"), agent("bbb2", "Plan")];
    expect(agentDisplayLabel("aaa1", agents)).toBe("Explore");
  });
});
