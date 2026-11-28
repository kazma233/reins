import type {
  BatchGitSkillImportResult,
  SkillDiscoveryResult,
  SkillSourceConfigView,
  SkillSourceKind,
} from "./types";
import { McpTransport } from "./types";

export const WORKSPACE_TAB_COPY = [
  {
    id: "skills",
    label: "skills",
    title: "技能分发",
    hint: "把 skill 以目录链接装进各个技能路径；来源与落点互不绑定",
  },
  {
    id: "mcp",
    label: "mcp",
    title: "MCP 服务",
    hint: "集中管理 MCP 服务，同步弹窗把勾选的服务收敛进各条 MCP 路径的配置文件",
  },
] as const;

export type SkillSourceFilter = "all" | "new" | `source:${string}`;

export type SkillTargetToggleKind = "install" | "replace" | "uninstall";

export type SkillDiscoveryPreviewState = {
  discovery: SkillDiscoveryResult | null;
  includeNamePatternsText: string;
  includePathPatternsText: string;
  previewLoading: boolean;
  // 预览读取失败：常驻在预览区域，下一次成功读取时清空。
  previewError: string | null;
};

type SkillSourcePreviewDraft = {
  sourceType: SkillSourceKind;
  repo: string;
  rootPath: string;
  ref: string;
};

// 导入弹窗的来源形态：git 与 local 是单个来源的两种录入方式，
// batch-git 用一份 JSON 一次声明多个 git 来源，三者互斥。
// 只有三态都是 git 录入形态时才涉及 ref 默认值，见 createSkillImportDialogState。
export type SkillImportSourceKind = SkillSourceKind | "batch-git";

export type SkillImportDialogState = {
  open: boolean;
  loading: boolean;
  sourceKind: SkillImportSourceKind;
  repo: string;
  rootPath: string;
  ref: string;
  // 单个导入的来源描述；批量导入的描述在 JSON 内容里，每个来源各一份。
  description: string;
  batchJsonText: string;
  // 最近一次选择的批量导入文件路径，仅在界面提示来源，提交只用内容。
  batchFilePath: string | null;
  batchResult: BatchGitSkillImportResult | null;
  preview: SkillDiscoveryPreviewState;
};

export type SkillDetailDialogState = {
  open: boolean;
  loading: boolean;
  detail: string;
  skillId: string | null;
};

export type SkillSourceEditDialogState = {
  open: boolean;
  loading: boolean;
  sourceId: string | null;
  title: string;
  // 用户手打的来源描述，保存时 trim，空串按清空提交。
  description: string;
  preview: SkillDiscoveryPreviewState;
} & SkillSourcePreviewDraft;

// 技能路径表单：条目名用户自由命名，目录可从预设回填或手填。
export type SkillPathFormState = {
  originalName: string | null;
  name: string;
  dir: string;
  errors: FieldErrors;
};

// MCP 路径表单：configType 创建时选定（预设带入或手选），更新保留存量值。
export type McpPathFormState = {
  originalName: string | null;
  name: string;
  configPath: string;
  configType: string;
  configPrefix: string;
  errors: FieldErrors;
};

export type FieldErrors = Partial<Record<string, string>>;

export type McpDeleteDialogState = {
  open: boolean;
  loading: boolean;
  error: string | null;
  serverNames: string[];
};

export type SkillPathDeleteDialogState = {
  open: boolean;
  loading: boolean;
  error: string | null;
  pathId: string | null;
  // 删除清理预演：将一并卸载的受管链接清单。
  cleanupPreview: string[] | null;
};

export type McpPathDeleteDialogState = {
  open: boolean;
  loading: boolean;
  error: string | null;
  pathId: string | null;
  // 删除清理预演：将一并移除的 MCP 条目名。
  cleanupServerNames: string[] | null;
};

export function parseCommaSeparatedList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function createSkillImportDialogState(sourceKind: SkillImportSourceKind = "git"): SkillImportDialogState {
  return {
    open: false,
    loading: false,
    sourceKind,
    repo: "",
    rootPath: "",
    // batch-git 不读该字段；默认 main 让 batch 切回 git 时分支输入框有默认值
    ref: sourceKind === "local" ? "" : "main",
    description: "",
    batchJsonText: "",
    batchFilePath: null,
    batchResult: null,
    preview: createSkillDiscoveryPreviewState(),
  };
}

// 把 git 来源列表序列化成批量导入 JSON（数组，一次导出全部远端来源）。
// schema 以 Rust 侧 BatchGitSkillImportSourceInput 为准（deny_unknown_fields，
// 键名 snake_case 且只允许这五个）：repo/ref 必填，description 与
// include patterns 为空时整个键省略；批量导入强制 ref 非空，来源未填分支时按
// 界面展示口径（ref ?? "main"）补默认值。没有 git 来源时返回 null。
export function buildSkillSourcesBatchJson(sources: SkillSourceConfigView[]): string | null {
  const entries = sources.flatMap((source) => {
    if (source.type !== "git") return [];

    const entry: Record<string, unknown> = {
      repo: source.repo,
      ref: source.ref?.trim() || "main",
    };
    const description = source.description?.trim();
    if (description) {
      entry.description = description;
    }
    if ((source.includeNamePatterns ?? []).length > 0) {
      entry.include_name_patterns = source.includeNamePatterns!;
    }
    if ((source.includePathPatterns ?? []).length > 0) {
      entry.include_path_patterns = source.includePathPatterns!;
    }
    return [entry];
  });
  if (entries.length === 0) return null;
  return JSON.stringify(entries, null, 2);
}

export function createSkillSourceEditDialogState(): SkillSourceEditDialogState {
  return {
    open: false,
    loading: false,
    sourceId: null,
    sourceType: "git",
    title: "",
    description: "",
    repo: "",
    rootPath: "",
    ref: "main",
    preview: createSkillDiscoveryPreviewState(),
  };
}

export function createSkillDiscoveryPreviewState(): SkillDiscoveryPreviewState {
  return {
    discovery: null,
    includeNamePatternsText: "",
    includePathPatternsText: "",
    previewLoading: false,
    previewError: null,
  };
}

export const DEFAULT_SKILL_PATH_FORM: SkillPathFormState = {
  originalName: null,
  name: "",
  dir: "",
  errors: {},
};

export const DEFAULT_MCP_PATH_FORM: McpPathFormState = {
  originalName: null,
  name: "",
  configPath: "",
  configType: "",
  configPrefix: "",
  errors: {},
};

// transport 的展示名（MCP 卡片与 MCP 路径弹窗共用）。
export function formatMcpTransport(transport: McpTransport): string {
  switch (transport) {
    case McpTransport.TransportStdio:
      return "本地命令（stdio）";
    case McpTransport.TransportHTTP:
      return "HTTP";
    case McpTransport.TransportSSE:
      return "SSE";
    default:
      return transport;
  }
}
