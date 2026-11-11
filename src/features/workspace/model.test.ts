import { describe, expect, it } from "vitest";
import { AGENT_IDS } from "@shared/lib/agent-labels";
import {
  buildSkillSourcesBatchJson,
  DEFAULT_PROJECT_FORM,
  formatTargetId,
  formatTargetLabel,
  formatTargetName,
  groupTargetIds,
  parseTargetId,
} from "./model";
import type { AgentTargetId, SkillSourceConfigView } from "./types";

describe("groupTargetIds", () => {
  it("puts global targets first and each project in its own group", () => {
    const groups = groupTargetIds([
      "codex",
      "web-apps:claude",
      "claude",
      "web-apps:codex",
      "reins:pi",
    ] as AgentTargetId[]);

    expect(groups).toEqual([
      { key: "global", label: "全局", targetIds: ["codex", "claude"] },
      { key: "web-apps", label: "web-apps", targetIds: ["web-apps:claude", "web-apps:codex"] },
      { key: "reins", label: "reins", targetIds: ["reins:pi"] },
    ]);
  });

  it("drops the global group when only project targets are installed", () => {
    const groups = groupTargetIds(["web-apps:claude"] as AgentTargetId[]);

    expect(groups.map((group) => group.key)).toEqual(["web-apps"]);
  });

  it("keeps the input order of agents inside a group", () => {
    const groups = groupTargetIds([
      "web-apps:codex",
      "web-apps:claude",
    ] as AgentTargetId[]);

    expect(groups[0].targetIds).toEqual(["web-apps:codex", "web-apps:claude"]);
  });
});

describe("parseTargetId", () => {
  it("splits a project target into both segments", () => {
    expect(parseTargetId("web-apps:codex")).toEqual({ projectId: "web-apps", agentId: "codex" });
  });

  it("treats an address without a separator as a global target", () => {
    expect(parseTargetId("codex")).toEqual({ projectId: null, agentId: "codex" });
  });

  it("matches the backend for empty segments and extra separators", () => {
    // 这组地址与后端 types.rs::target_ref_rejects_empty_segments 是同一份
    // 用例集，两边必须给出同一答案。
    expect(parseTargetId(":codex")).toEqual({ projectId: null, agentId: ":codex" });
    expect(parseTargetId("demo:")).toEqual({ projectId: null, agentId: "demo:" });
    expect(parseTargetId(":")).toEqual({ projectId: null, agentId: ":" });

    // 第二段含 `:` 后端照样接受：split_once 后两段都非空即为项目地址。
    expect(parseTargetId("a:b:codex")).toEqual({ projectId: "a", agentId: "b:codex" });
  });
});

describe("formatTargetId", () => {
  it("round-trips through parseTargetId", () => {
    // 与 parseTargetId 互逆是这对函数存在的意义:格式只有这一对所有者。
    expect(parseTargetId(formatTargetId("web-apps", "codex"))).toEqual({
      projectId: "web-apps",
      agentId: "codex",
    });
    expect(formatTargetId("reins", "pi")).toBe("reins:pi" as AgentTargetId);
  });
});

describe("formatTargetLabel", () => {
  it("renders canonical product names instead of the raw config ids", () => {
    expect(formatTargetLabel("claude")).toBe("Claude Code");
    expect(formatTargetLabel("zcode")).toBe("ZCode");
    expect(formatTargetLabel("dsh")).toBe("DeepSeek Harness");
  });

  it("keeps the project prefix on project targets", () => {
    expect(formatTargetLabel("web-apps:codex")).toBe("web-apps/Codex");
  });

  it("falls back to the raw id for user-defined targets", () => {
    expect(formatTargetLabel("my-gateway")).toBe("my-gateway");
  });
});

describe("formatTargetName", () => {
  it("renders every built-in agent with a canonical name, never the raw id", () => {
    for (const agentId of AGENT_IDS) {
      const label = formatTargetName(agentId);
      expect(label).not.toBe(agentId);
    }
  });
});

describe("DEFAULT_PROJECT_FORM", () => {
  it("starts without any agent enabled", () => {
    expect(DEFAULT_PROJECT_FORM.agents).toEqual([]);
  });
});

describe("buildSkillSourcesBatchJson", () => {
  const gitSource: SkillSourceConfigView = {
    type: "git",
    id: "s1",
    repo: "https://github.com/anthropics/skills.git",
    ref: "main",
    description: "anthropics/skills",
    includeNamePatterns: ["agent-*"],
    includePathPatterns: ["python/*"],
    label: "anthropics/skills",
  };
  const localSource: SkillSourceConfigView = {
    type: "local",
    id: "s2",
    rootPath: "~/skills",
    description: null,
    includeNamePatterns: [],
    includePathPatterns: [],
    label: "~/skills",
  };

  it("serializes git sources into the batch import json schema", () => {
    const secondGit: SkillSourceConfigView = {
      ...gitSource,
      id: "s3",
      repo: "https://example.com/repo.git",
      ref: null,
      description: "  示例仓库  ",
      includeNamePatterns: [],
      includePathPatterns: [],
    };
    expect(buildSkillSourcesBatchJson([gitSource, localSource, secondGit])).toBe(
      JSON.stringify(
        [
          {
            repo: "https://github.com/anthropics/skills.git",
            ref: "main",
            description: "anthropics/skills",
            include_name_patterns: ["agent-*"],
            include_path_patterns: ["python/*"],
          },
          // local 来源被过滤；第二个 git 来源描述 trim 后写入、
          // 空 patterns 省略键、ref 按行内展示口径补 main
          { repo: "https://example.com/repo.git", ref: "main", description: "示例仓库" },
        ],
        null,
        2,
      ),
    );
  });

  it("returns null when no git source is present", () => {
    expect(buildSkillSourcesBatchJson([localSource])).toBeNull();
    expect(buildSkillSourcesBatchJson([])).toBeNull();
  });
});
