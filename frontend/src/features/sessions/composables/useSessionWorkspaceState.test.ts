import { describe, expect, it } from "vitest";
import { ref } from "vue";
import { useSessionWorkspaceState } from "./useSessionWorkspaceState";
import { ALL_SOURCES } from "../source-app";
import { SourceApp, type SessionSummary } from "../types";

function summary(sourceApp: SourceApp, sourceSessionId: string): SessionSummary {
  return {
    sourceApp,
    sourceSessionId,
    title: sourceSessionId,
    cwd: null,
    gitBranch: null,
    transcriptPath: `/tmp/${sourceApp}/${sourceSessionId}.jsonl`,
    createdAt: 0,
    updatedAt: null,
    models: [],
    archived: false,
    compressed: false
  };
}

describe("useSessionWorkspaceState", () => {
  it("switching source invalidates the list, selection and retained detail", () => {
    const store = useSessionWorkspaceState();
    const codexA = summary(SourceApp.SourceCodex, "a");
    store.setSessions([codexA]);
    store.setSelectedSessionKey("codex:a");
    store.setSessionOverview({
      summary: codexA,
      sourcePaths: [codexA.transcriptPath],
      messageCount: null,
      eventCount: null,
      agents: []
    });

    store.setSelectedSource(SourceApp.SourcePi);

    expect(store.selectedSource.value).toBe("pi");
    expect(store.sessions.value).toEqual([]);
    expect(store.selectedSessionKey.value).toBeNull();
    expect(store.sessionOverview.value).toBeNull();
  });

  it("re-selecting the current source is a no-op that keeps the data", () => {
    const store = useSessionWorkspaceState();
    const codexA = summary(SourceApp.SourceCodex, "a");
    store.setSessions([codexA]);
    store.setSelectedSessionKey("codex:a");

    store.setSelectedSource(ALL_SOURCES);
    expect(store.selectedSource.value).toBe("all");
    store.setSelectedSource(ALL_SOURCES);

    expect(store.sessions.value).toEqual([codexA]);
    expect(store.selectedSessionKey.value).toBe("codex:a");
  });

  it("appendSessions dedups by session identity so paging stays stable", () => {
    const store = useSessionWorkspaceState();
    const first = summary(SourceApp.SourceCodex, "a");
    const refreshedFirst = { ...first, title: "refreshed" };
    const second = summary(SourceApp.SourceCodex, "b");

    store.setSessions([first]);
    store.appendSessions([refreshedFirst, second]);

    expect(store.sessions.value).toEqual([refreshedFirst, second]);
  });
});
