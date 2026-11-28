import { describe, expect, it } from "vitest";

import type { WorkspaceInspection } from "./types";
import { buildSkillDirLinkMap, skillDirLinkLookup } from "./skillDirLink";

function inspection(overrides: Partial<WorkspaceInspection> = {}): WorkspaceInspection {
  return {
    configPath: "/home/.reins/config.yaml",
    skillPaths: [],
    mcps: [],
    warnings: [],
    ...overrides,
  };
}

describe("skill path list dir attribution", () => {
  it("maps skill paths to their inspection entries", () => {
    const map = buildSkillDirLinkMap(
      inspection({
        skillPaths: [
          {
            name: "codex",
            dirExists: true,
            dirLink: null,
          },
          {
            name: "alias",
            dirExists: true,
            dirLink: { linkedPathId: "codex", linkTarget: "/home/.codex/skills" },
          },
        ],
      }),
    );

    // 无软链接的行也在表里(place 为空):目录不存在的提示靠它,不依赖链接。
    expect(skillDirLinkLookup(map, "codex")).toEqual({ place: "", exists: true });
    expect(skillDirLinkLookup(map, "alias")).toEqual({
      place: "/home/.codex/skills",
      exists: true,
    });
  });

  it("carries existence so the panel can flag a missing dir", () => {
    const map = buildSkillDirLinkMap(
      inspection({
        skillPaths: [
          {
            name: "ghost",
            dirExists: false,
            dirLink: null,
          },
        ],
      }),
    );

    expect(skillDirLinkLookup(map, "ghost")).toEqual({ place: "", exists: false });
  });

  it("returns nothing when the inspection is unavailable", () => {
    const map = buildSkillDirLinkMap(null);
    expect(skillDirLinkLookup(map, "codex")).toBeNull();
  });
});
