import { computed } from "vue";

import type { WorkspaceInspection } from "./types";

export type SkillDirLinkLookup = {
  // 链接指向的原始目的地：界面显示的就是「这个链接挂在哪」。落点不是
  // 软链接时为空串。
  place: string;
  // 技能路径的目录在盘上是否存在：可能是待安装，也可能是路径写错——
  // 界面中性提示，不下结论。
  exists: boolean;
};

// 技能路径列表的 inspection 行索引：按条目名查落点归属与存在性，与后端
// inspection 的条目名口径一致。
export function buildSkillDirLinkMap(
  inspection: WorkspaceInspection | null,
): Map<string, SkillDirLinkLookup> {
  const map = new Map<string, SkillDirLinkLookup>();
  if (!inspection) return map;
  for (const path of inspection.skillPaths ?? []) {
    map.set(path.name, {
      place: path.dirLink?.linkTarget ?? "",
      exists: path.dirExists,
    });
  }
  return map;
}

// 落点归属只给链接目的地：它就是「这个别名挂在哪儿」的答案，属主名字是多余的
// 一层翻译（要看属主，目的地路径里已经写着）。
export function skillDirLinkLookup(
  map: Map<string, SkillDirLinkLookup>,
  pathName: string,
): SkillDirLinkLookup | null {
  return map.get(pathName) ?? null;
}

// 面板里按条目名取一次的便捷封装。
export function useSkillDirLinks(inspection: () => WorkspaceInspection | null) {
  const map = computed(() => buildSkillDirLinkMap(inspection()));
  return (pathName: string) => skillDirLinkLookup(map.value, pathName);
}
