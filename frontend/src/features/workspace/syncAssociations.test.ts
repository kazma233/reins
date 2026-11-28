import { describe, expect, it } from "vitest";
import { SkillLinkState, type SkillLinkAssociation, type SyncPathOption } from "./types";
import {
  buildSkillLinkSourceLabels,
  canRemoveSkillLink,
  defaultSelectedSyncPathIds,
  hasCurrentSourceSkillLinks,
  hasUnmanagedSkillLinks,
  isSelectableSyncPath,
  pendingSkillLinkCleanupCount,
  skillLinkSourceLabel,
  skillLinkSourcePath,
  skillLinkStateInfo,
} from "./syncAssociations";

function makeLink(state: SkillLinkAssociation["state"], matchedSourceIds: string[] = []): SkillLinkAssociation {
  return {
    skillName: "alpha",
    destinationPath: "/target/alpha",
    sourcePath: "/source/alpha",
    matchedSourceIds,
    sourceRelatives: [],
    state,
  };
}

function makePath(overrides: Partial<SyncPathOption> & { name: string }): SyncPathOption {
  // overrides 展开在前：未覆盖的键保持缺席，下游断言依赖这个运行时形态。
  const path = {
    dir: `/paths/${overrides.name}`,
    dirLinkTarget: null,
    linkedPathId: null,
    links: [],
    ...overrides,
  };
  return path as SyncPathOption;
}



describe("skill link association presentation", () => {
  it("labels every scanned link state", () => {
    expect(skillLinkStateInfo(SkillLinkState.LinkStateLinked)).toMatchObject({ label: "已关联", pendingCleanup: false });
    expect(skillLinkStateInfo(SkillLinkState.LinkStateExcluded)).toMatchObject({ pendingCleanup: true });
    expect(skillLinkStateInfo(SkillLinkState.LinkStateSourceMissed)).toMatchObject({ pendingCleanup: true });
    expect(skillLinkStateInfo(SkillLinkState.LinkStateUnmanaged)).toMatchObject({ label: "非受管来源", pendingCleanup: false });
  });

  it("counts only excluded and missing source links as pending cleanup", () => {
    const links = [
      makeLink(SkillLinkState.LinkStateLinked),
      makeLink(SkillLinkState.LinkStateExcluded),
      makeLink(SkillLinkState.LinkStateSourceMissed),
      makeLink(SkillLinkState.LinkStateUnmanaged),
    ];

    expect(pendingSkillLinkCleanupCount(links)).toBe(2);
  });

  it("keeps every matching source label when source roots overlap", () => {
    expect(skillLinkSourceLabel(makeLink(SkillLinkState.LinkStateLinked, ["shared", "nested"]))).toBe("shared、nested");
    expect(skillLinkSourceLabel(makeLink(SkillLinkState.LinkStateLinked, ["shared", "nested"]), "shared")).toBe(
      "当前来源 · nested",
    );
    // 纯当前来源的链接不重复「当前来源」文字,行底色已承载归属
    expect(skillLinkSourceLabel(makeLink(SkillLinkState.LinkStateLinked, ["shared"]), "shared")).toBe("");
    expect(skillLinkSourceLabel(makeLink(SkillLinkState.LinkStateUnmanaged))).toBe("");
  });

  it("uses readable owner/repo labels for configured git sources", () => {
    const sourceLabels = buildSkillLinkSourceLabels([
      {
        type: "git",
        id: "source-a",
        repo: "https://github.com/acme/skills.git",
        ref: "main",
        includeNamePatterns: [],
        includePathPatterns: [],
        label: "https://github.com/acme/skills.git · main",
      },
      {
        type: "git",
        id: "source-b",
        repo: "git@github.com:other/tools.git",
        ref: "main",
        includeNamePatterns: [],
        includePathPatterns: [],
        label: "git@github.com:other/tools.git · main",
      },
    ]);

    expect(skillLinkSourceLabel(makeLink(SkillLinkState.LinkStateLinked, ["source-a", "source-b"]), "source-a", sourceLabels)).toBe(
      "当前来源 · other/tools",
    );
    expect(skillLinkSourceLabel(makeLink(SkillLinkState.LinkStateLinked, ["source-a", "source-b"]), undefined, sourceLabels)).toBe(
      "acme/skills、other/tools",
    );
  });

  it("detects path-level unmanaged and current-source tags", () => {
    const links = [
      makeLink(SkillLinkState.LinkStateUnmanaged),
      makeLink(SkillLinkState.LinkStateExcluded, ["source-a"]),
    ];

    expect(hasUnmanagedSkillLinks(links)).toBe(true);
    expect(hasCurrentSourceSkillLinks(links, "source-a")).toBe(true);
    expect(hasCurrentSourceSkillLinks(links, "source-b")).toBe(false);
    expect(hasCurrentSourceSkillLinks(links)).toBe(false);
  });

  it("marks alias dirs as unselectable", () => {
    expect(isSelectableSyncPath(makePath({ name: "plain" }))).toBe(true);
    expect(isSelectableSyncPath(makePath({ name: "inherited", linkedPathId: "plain" }))).toBe(false);
    // 链接另一端不是受管路径（外部目录）时 linkedPathId 为空，但目录同样是
    // 别名——此前它会保持可选，勾了之后后端静默跳过（用户可见的静默失效）。
    expect(isSelectableSyncPath(makePath({ name: "external", dirLinkTarget: "/external/skills" }))).toBe(false);
    // 断链：canonicalize 失败时后端回落为解析出的原路径，仍有值。
    const broken = makePath({ name: "broken", dirLinkTarget: "/gone/skills" });
    expect(isSelectableSyncPath(broken)).toBe(false);
    // 界面只显示链接目的地（属主名字那一层翻译已去掉）。
    expect(broken.dirLinkTarget).toBe("/gone/skills");
    expect(makePath({ name: "plain" }).dirLinkTarget).toBeNull();
  });

  it("preselects selectable paths already linked to the current source", () => {
    const targets = [
      makePath({ name: "linked", links: [makeLink(SkillLinkState.LinkStateLinked, ["source-a"])] }),
      makePath({
        name: "cleanup",
        links: [makeLink(SkillLinkState.LinkStateExcluded, ["source-a"]), makeLink(SkillLinkState.LinkStateSourceMissed, ["source-a"])],
      }),
      makePath({ name: "other-source", links: [makeLink(SkillLinkState.LinkStateLinked, ["source-b"])] }),
      makePath({ name: "unmanaged", links: [makeLink(SkillLinkState.LinkStateUnmanaged)] }),
      makePath({ name: "empty" }),
      makePath({ name: "inherited", linkedPathId: "linked", links: [makeLink(SkillLinkState.LinkStateLinked, ["source-a"])] }),
    ];

    expect(defaultSelectedSyncPathIds(targets, "source-a")).toEqual(["linked", "cleanup"]);
    expect(defaultSelectedSyncPathIds(targets)).toEqual([]);
  });

  it("allows removing only unmanaged links one by one", () => {
    expect(canRemoveSkillLink(makeLink(SkillLinkState.LinkStateUnmanaged))).toBe(true);
    expect(canRemoveSkillLink(makeLink(SkillLinkState.LinkStateLinked, ["source-a"]))).toBe(false);
    expect(canRemoveSkillLink(makeLink(SkillLinkState.LinkStateExcluded, ["source-a"]))).toBe(false);
    expect(canRemoveSkillLink(makeLink(SkillLinkState.LinkStateSourceMissed, ["source-a"]))).toBe(false);
  });

  it("takes the current source's relative path from the backend payload", () => {
    // 相对路径由后端 classify 统一算出(与安装面同一套口径),前端只按
    // 当前来源查表;查不到退回绝对路径。
    const link: SkillLinkAssociation = {
      ...makeLink(SkillLinkState.LinkStateLinked, ["source-a", "source-b"]),
      sourceRelatives: [
        { sourceId: "source-a", relativePath: "nested/alpha" },
        { sourceId: "source-b", relativePath: "elsewhere/alpha" },
      ],
    };
    expect(skillLinkSourcePath(link, "source-a")).toBe("nested/alpha");
    expect(skillLinkSourcePath(link, "source-b")).toBe("elsewhere/alpha");
    expect(skillLinkSourcePath(link, "source-c")).toBe("/source/alpha");
    expect(skillLinkSourcePath(link)).toBe("/source/alpha");
  });
});
