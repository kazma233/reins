import { describe, expect, it } from "vitest";
import { ALL_SOURCES } from "./source-app";
import { SourceApp, type SessionOverview, type SessionSummary } from "./types";
import {
  canLoadDetail,
  placeholderOverview,
  resolveSelectedSummary
} from "./session-selection";

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

const codexA = summary(SourceApp.SourceCodex, "a");
const claudeB = summary(SourceApp.SourceClaudeCode, "b");

function overviewOf(current: SessionSummary): SessionOverview {
  return placeholderOverview(current);
}

describe("resolveSelectedSummary", () => {
  it("returns null without a selection", () => {
    expect(resolveSelectedSummary([codexA], null, null)).toBeNull();
  });

  it("prefers the visible list entry matched by identity key", () => {
    expect(resolveSelectedSummary([codexA, claudeB], null, "codex:a")).toBe(codexA);
  });

  it("falls back to the retained overview summary between list reloads", () => {
    expect(
      resolveSelectedSummary([], overviewOf(claudeB), "claude_code:b")
    ).toBe(claudeB);
  });

  it("returns null when the retained summary belongs to another session", () => {
    expect(resolveSelectedSummary([], overviewOf(claudeB), "codex:a")).toBeNull();
  });
});

describe("canLoadDetail", () => {
  it("rejects a missing summary regardless of the source view", () => {
    expect(canLoadDetail(null, ALL_SOURCES)).toBe(false);
  });

  it("merged view loads detail from the session's own source", () => {
    expect(canLoadDetail(claudeB, ALL_SOURCES)).toBe(true);
  });

  it("single-source view requires the summary to match the source", () => {
    expect(canLoadDetail(codexA, SourceApp.SourceCodex)).toBe(true);
    expect(canLoadDetail(claudeB, SourceApp.SourceCodex)).toBe(false);
  });
});

describe("placeholderOverview", () => {
  it("builds the skeleton: summary plus transcript path, counts pending", () => {
    expect(placeholderOverview(codexA)).toEqual({
      summary: codexA,
      sourcePaths: [codexA.transcriptPath],
      messageCount: null,
      eventCount: null,
      agents: []
    });
  });
});
