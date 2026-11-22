import { beforeEach, describe, expect, it, vi } from "vitest";
import { ALL_MODELS, ALL_SOURCES } from "../model";
import { SourceApp } from "../types";
import { useUsageState } from "./useUsageState";

// Only getUsageStats is reachable from useUsageState's import graph;
// mocking the module keeps the backend bindings out of tests.
vi.mock("../api", () => ({
  getUsageStats: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

describe("useUsageState 筛选", () => {
  it("切换来源会把模型筛选重置回全部", () => {
    const { selectedSource, selectedModel, setSelectedSource, setSelectedModel } =
      useUsageState();
    expect(selectedSource.value).toBe(ALL_SOURCES);
    expect(selectedModel.value).toBe(ALL_MODELS);

    setSelectedModel("gpt-5.3");
    setSelectedSource(SourceApp.SourceCodex);

    expect(selectedSource.value).toBe("codex");
    expect(selectedModel.value).toBe(ALL_MODELS);
  });
});
