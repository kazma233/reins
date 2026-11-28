import { describe, expect, it } from "vitest";
import {
  DEFAULT_MCP_PATH_FORM,
  DEFAULT_SKILL_PATH_FORM,
  buildSkillSourcesBatchJson,
  formatMcpTransport,
} from "./model";
import { McpTransport, type SkillSourceConfigView } from "./types";

describe("DEFAULT_SKILL_PATH_FORM", () => {
  it("starts without prefilling any tool", () => {
    // 新装空开始：表单不替用户预设一个他可能没装的路径。
    expect(DEFAULT_SKILL_PATH_FORM).toEqual({
      originalName: null,
      name: "",
      dir: "",
      errors: {},
    });
  });
});

describe("DEFAULT_MCP_PATH_FORM", () => {
  it("starts without prefilling any tool", () => {
    expect(DEFAULT_MCP_PATH_FORM).toEqual({
      originalName: null,
      name: "",
      configPath: "",
      configType: "",
      configPrefix: "",
      errors: {},
    });
  });
});

describe("formatMcpTransport", () => {
  it("renders canonical labels for every transport", () => {
    expect(formatMcpTransport(McpTransport.TransportStdio)).toBe("本地命令（stdio）");
    expect(formatMcpTransport(McpTransport.TransportHTTP)).toBe("HTTP");
    expect(formatMcpTransport(McpTransport.TransportSSE)).toBe("SSE");
  });
});

describe("buildSkillSourcesBatchJson", () => {
  function gitSource(overrides: Partial<SkillSourceConfigView> = {}): SkillSourceConfigView {
    return {
      type: "git",
      id: "s1",
      repo: "https://github.com/acme/skills.git",
      ref: "main",
      includeNamePatterns: [],
      includePathPatterns: [],
      label: "",
      ...overrides,
    };
  }

  it("serializes git sources into the batch import json schema", () => {
    const json = buildSkillSourcesBatchJson([
      gitSource({
        description: " 官方技能库 ",
        includeNamePatterns: ["archive/*"],
        includePathPatterns: ["docs/**"],
      }),
    ]);
    expect(json).toBe(
      JSON.stringify(
        [
          {
            repo: "https://github.com/acme/skills.git",
            ref: "main",
            description: "官方技能库",
            include_name_patterns: ["archive/*"],
            include_path_patterns: ["docs/**"],
          },
        ],
        null,
        2,
      ),
    );
  });

  it("omits blank description and empty patterns, defaults ref to main", () => {
    const json = buildSkillSourcesBatchJson([gitSource({ ref: null, description: "  " })]);
    expect(json).toBe(
      JSON.stringify(
        [{ repo: "https://github.com/acme/skills.git", ref: "main" }],
        null,
        2,
      ),
    );
  });

  it("returns null when there is no git source", () => {
    expect(
      buildSkillSourcesBatchJson([
        {
          type: "local",
          id: "s2",
          rootPath: "/tmp/notes",
          includeNamePatterns: [],
          includePathPatterns: [],
          label: "/tmp/notes",
        },
      ]),
    ).toBeNull();
  });
});
