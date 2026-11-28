import { SkillLinkState, type SkillLinkAssociation, type SkillSourceConfigView, type SyncPathOption } from "./types";

export type SkillLinkStateInfo = {
  label: string;
  tone: "linked" | "cleanup" | "unmanaged";
  pendingCleanup: boolean;
};

// $zero 是 bindings 枚举的 Go 零值占位，wire 上不会有意出现；映射为中性降级值。
const STATE_INFO: Record<SkillLinkAssociation["state"], SkillLinkStateInfo> = {
  [SkillLinkState.LinkStateLinked]: { label: "已关联", tone: "linked", pendingCleanup: false },
  [SkillLinkState.LinkStateExcluded]: { label: "待清理: 已排除", tone: "cleanup", pendingCleanup: true },
  [SkillLinkState.LinkStateSourceMissed]: { label: "待清理: 源已删除", tone: "cleanup", pendingCleanup: true },
  [SkillLinkState.LinkStateUnmanaged]: { label: "非受管来源", tone: "unmanaged", pendingCleanup: false },
  [SkillLinkState.$zero]: { label: "", tone: "unmanaged", pendingCleanup: false },
};

export function skillLinkStateInfo(state: SkillLinkAssociation["state"]): SkillLinkStateInfo {
  return STATE_INFO[state];
}

export function skillLinkSourceLabel(
  link: SkillLinkAssociation,
  currentSourceId?: string,
  sourceLabels: Record<string, string> = {},
): string {
  const matchedSourceIds = link.matchedSourceIds ?? [];
  if (matchedSourceIds.length === 0) return "";

  const getSourceLabel = (sourceId: string) => sourceLabels[sourceId] ?? sourceId;

  if (currentSourceId && matchedSourceIds.includes(currentSourceId)) {
    const otherSourceIds = matchedSourceIds.filter((sourceId) => sourceId !== currentSourceId);
    if (otherSourceIds.length === 0) {
      // 纯当前来源的链接:行底色已标出归属,不再重复一行「当前来源」。
      // 混合归属时仍要报出其它来源的名字,那是别处看不到的信息。
      return "";
    }
    return `当前来源 · ${otherSourceIds.map(getSourceLabel).join("、")}`;
  }

  return matchedSourceIds.map(getSourceLabel).join("、");
}

function gitOwnerRepo(repo: string): string {
  const normalized = repo
    .trim()
    .replace(/[\\/]+$/, "")
    .replace(/\.git$/i, "");
  const parts = normalized.split(/[\\/:]/).filter(Boolean);
  return parts.length >= 2 ? `${parts[parts.length - 2]}/${parts[parts.length - 1]}` : normalized;
}

export function buildSkillLinkSourceLabels(
  sources: SkillSourceConfigView[],
): Record<string, string> {
  return Object.fromEntries(
    sources.map((source) => [source.id, source.type === "git" ? gitOwnerRepo(source.repo ?? "") : source.label]),
  );
}

export function hasUnmanagedSkillLinks(links: SkillLinkAssociation[]): boolean {
  return links.some((link) => link.state === SkillLinkState.LinkStateUnmanaged);
}

export function hasCurrentSourceSkillLinks(
  links: SkillLinkAssociation[],
  currentSourceId?: string,
): boolean {
  return Boolean(
    currentSourceId && links.some((link) => (link.matchedSourceIds ?? []).includes(currentSourceId)),
  );
}

// 目录是软链接的路径：那个目录不属于它，内容由链接另一端负责，因此不参与
// 同步选择。判定只看「是不是链接」——能叫出归属（linkedPathId）与叫不出
// （外部目录、断链）一样不可选，后者此前会静默失效。
export function skillDirIsAlias(path: SyncPathOption): boolean {
  return Boolean(path.dirLinkTarget ?? path.linkedPathId);
}

// 能被勾选的路径：目录是软链接（内容归另一条路径或外部）的都不参与同步选择。
export function isSelectableSyncPath(path: SyncPathOption): boolean {
  return !skillDirIsAlias(path);
}

// 可勾选路径的 id 列表：同步弹窗与路径分组组件的全选/计数共用同一份判定。
export function selectableSyncPathIds(paths: SyncPathOption[]): string[] {
  return paths.filter(isSelectableSyncPath).map((path) => path.name);
}

// 打开同步弹窗时的路径预选：只挑已经装了当前来源内容的那批（列表里带
// 「关联」标签的路径），让用户接着把来源新增的 skill 补过去，不用每次重新勾。
export function defaultSelectedSyncPathIds(
  paths: SyncPathOption[],
  currentSourceId?: string,
): string[] {
  return paths
    .filter(isSelectableSyncPath)
    .filter((path) => hasCurrentSourceSkillLinks(path.links ?? [], currentSourceId))
    .map((path) => path.name);
}

// 只有非受管来源的链接可以逐条移除：它们不属于任何已配置来源，没有来源级的
// 清理入口；待清理项仍归属当前来源，由移除同步或下次同步统一处理。
export function canRemoveSkillLink(link: SkillLinkAssociation): boolean {
  return link.state === SkillLinkState.LinkStateUnmanaged;
}

// 展示链接的源路径:按当前来源取后端统一下发的相对路径(与安装面同一套
// Windows/大小写口径),查不到(非归属来源、未指定)退回绝对路径。此前的
// 前端归一化(Windows 前缀剥离、大小写折叠、尾斜杠清理)是对后端 classify
// 已算出值的重实现,已删。
export function skillLinkSourcePath(
  link: SkillLinkAssociation,
  currentSourceId?: string,
): string {
  if (!currentSourceId) return link.sourcePath;
  return (
    (link.sourceRelatives ?? []).find((relative) => relative.sourceId === currentSourceId)
      ?.relativePath ?? link.sourcePath
  );
}

export function pendingSkillLinkCleanupCount(links: SkillLinkAssociation[]): number {
  return links.filter((link) => skillLinkStateInfo(link.state).pendingCleanup).length;
}
