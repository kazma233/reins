import { reactive } from "vue";
import {
  createMcpPath,
  deleteMcpPath,
  getMcpFormatInfos,
  getMcpPathPresets,
  previewMcpPathCleanup,
  selectMcpPathConfigFile,
  updateMcpPath,
} from "../api";
import {
  DEFAULT_MCP_PATH_FORM,
  type FieldErrors,
  type McpPathDeleteDialogState,
  type McpPathFormState,
} from "../model";
import type { McpFormatExample, McpFormatInfo, McpPathPreset, McpPathView } from "../types";
import { useWorkspaceAction } from "./useWorkspaceAction";
import type { WorkspaceStateContext } from "./useWorkspaceState";

function buildMcpPathErrors(form: McpPathFormState): FieldErrors {
  const errors: FieldErrors = {};
  if (!form.name.trim()) {
    errors.name = "请填写条目名。";
  }
  if (!form.configPath.trim()) {
    errors.configPath = "请填写 MCP 配置文件路径，或先选一个预设。";
  }
  // 创建模式必须选定格式（预设带入或手选）；更新模式保留存量值不传。
  if (form.originalName === null && !form.configType.trim()) {
    errors.configType = "请选择 MCP 配置格式。";
  }
  // configPrefix 与格式的成对校验由后端按实际格式执行
  //（dsh/hermes 等格式允许空 prefix），错误信息直接展示在弹窗错误区。
  return errors;
}

export function useMcpPathMutations(workspace: WorkspaceStateContext) {
  const { runWorkspaceAction } = useWorkspaceAction(workspace);

  const mcpPathDialog = reactive<{
    open: boolean;
    loading: boolean;
    error: string | null;
    form: McpPathFormState;
    // 内置工具预设由后端下发（路径是 env 解析后的真实值），仅创建模式使用。
    presets: McpPathPreset[];
    presetsLoading: boolean;
    presetsError: string | null;
    // 全部配置格式的说明与写入示例（创建弹窗格式选择区的展开内容）。
    formatInfos: McpFormatInfo[];
    formatInfosLoading: boolean;
    formatInfosError: string | null;
    // 当前选中格式的说明与写入形态示例：创建模式跟随选择的 configType，
    // 编辑模式来自存量条目的视图。
    formatDescription: string | null;
    formatExamples: McpFormatExample[];
  }>({
    open: false,
    loading: false,
    error: null,
    form: { ...DEFAULT_MCP_PATH_FORM, errors: {} },
    presets: [],
    presetsLoading: false,
    presetsError: null,
    formatInfos: [],
    formatInfosLoading: false,
    formatInfosError: null,
    formatDescription: null,
    formatExamples: [],
  });

  const mcpPathDeleteDialog = reactive<McpPathDeleteDialogState>({
    open: false,
    loading: false,
    error: null,
    pathId: null,
    cleanupServerNames: null,
  });

  function openMcpPathCreateDialog(): Promise<void> {
    mcpPathDialog.form = { ...DEFAULT_MCP_PATH_FORM, errors: {} };
    mcpPathDialog.error = null;
    mcpPathDialog.formatDescription = null;
    mcpPathDialog.formatExamples = [];
    mcpPathDialog.open = true;
    void loadMcpPathPresets();
    return loadMcpFormatInfos();
  }

  function openMcpPathEditDialog(path: McpPathView) {
    mcpPathDialog.form = {
      originalName: path.name,
      name: path.name,
      configPath: path.configPath ?? "",
      configType: "",
      configPrefix: path.configPrefix,
      errors: {},
    };
    mcpPathDialog.error = null;
    mcpPathDialog.formatDescription = path.mcpFormatDescription;
    mcpPathDialog.formatExamples = path.mcpFormatExamples ?? [];
    mcpPathDialog.open = true;
  }

  function closeMcpPathDialog() {
    mcpPathDialog.open = false;
    mcpPathDialog.error = null;
    mcpPathDialog.formatDescription = null;
    mcpPathDialog.formatExamples = [];
    mcpPathDialog.form = { ...DEFAULT_MCP_PATH_FORM, errors: {} };
  }

  function clearMcpPathFormError(field: string) {
    const errors = mcpPathDialog.form.errors;
    if (!errors[field]) return;
    const next = { ...errors };
    delete next[field];
    mcpPathDialog.form.errors = next;
  }

  async function loadMcpPathPresets() {
    mcpPathDialog.presetsLoading = true;
    mcpPathDialog.presetsError = null;
    await runWorkspaceAction({
      action: () => getMcpPathPresets(),
      pageLock: false,
      error: "读取预设失败。",
      onSuccess: (presets) => {
        mcpPathDialog.presets = presets;
      },
      onError: (message) => {
        mcpPathDialog.presetsError = message;
      },
    });
    mcpPathDialog.presetsLoading = false;
  }

  // 弹窗打开时拉取格式说明与示例（创建弹窗格式选择区的展开内容）；失败
  // 常驻在选择区并给重试入口。拉取不占整页忙碌态。
  async function loadMcpFormatInfos() {
    mcpPathDialog.formatInfosLoading = true;
    mcpPathDialog.formatInfosError = null;
    await runWorkspaceAction({
      action: () => getMcpFormatInfos(),
      pageLock: false,
      error: "读取配置格式失败。",
      onSuccess: (infos) => {
        mcpPathDialog.formatInfos = infos;
      },
      onError: (message) => {
        mcpPathDialog.formatInfosError = message;
      },
    });
    mcpPathDialog.formatInfosLoading = false;
  }

  // 点击预设即回填表单。
  function handleApplyMcpPathPreset(presetName: string) {
    const preset = mcpPathDialog.presets.find((item) => item.name === presetName);
    if (!preset) return;
    Object.assign(mcpPathDialog.form, {
      name: preset.name,
      configPath: preset.configPath,
      configType: preset.configType,
      configPrefix: preset.configPrefix,
      errors: {},
    });
    mcpPathDialog.formatDescription = null;
    mcpPathDialog.formatExamples = [];
  }

  // 切换格式：只换格式知识，不动已填的路径；前缀仅在为空或还是别格式的
  // 约定值时跟随新格式回落（该格式的代表前缀 = 清单里首个该格式工具的
  // 约定值），用户手填的自定义前缀不会被覆盖。
  function handleSelectMcpPathConfigType(configType: string) {
    mcpPathDialog.form.configType = configType;
    const representative = mcpPathDialog.presets.find(
      (item) => item.configType === configType,
    );
    const currentPrefix = mcpPathDialog.form.configPrefix.trim();
    const belongsToOtherType = mcpPathDialog.presets.some(
      (item) => item.configPrefix === currentPrefix && item.configPrefix !== "" && item.configType !== configType,
    );
    if (currentPrefix === "" || belongsToOtherType) {
      mcpPathDialog.form.configPrefix = representative?.configPrefix ?? "";
    }
  }

  async function handlePickMcpPathConfigFile() {
    await runWorkspaceAction({
      action: async () => {
        const selected = await selectMcpPathConfigFile(
          mcpPathDialog.form.configPath.trim() || undefined,
        );
        if (!selected) return;
        mcpPathDialog.form.configPath = selected.workspaceDir;
        clearMcpPathFormError("configPath");
      },
      error: "选择 MCP 配置文件失败。",
      onError: (message) => {
        mcpPathDialog.error = message;
      },
    });
  }

  async function handleSubmitMcpPath() {
    const form = mcpPathDialog.form;
    const errors = buildMcpPathErrors(form);
    form.errors = errors;
    if (Object.keys(errors).length > 0) {
      return;
    }

    const name = form.name.trim();
    const originalName = form.originalName;
    const isEdit = originalName !== null;

    mcpPathDialog.loading = true;
    await runWorkspaceAction({
      action: () =>
        isEdit
          ? updateMcpPath(originalName, name, form.configPath.trim(), form.configPrefix.trim())
          : createMcpPath(name, form.configPath.trim(), form.configType.trim(), form.configPrefix.trim()),
      reload: true,
      error: isEdit ? "更新 MCP 路径失败。" : "添加 MCP 路径失败。",
      onSuccess: () => closeMcpPathDialog(),
      onError: (message) => {
        mcpPathDialog.error = message;
      },
    });
    mcpPathDialog.loading = false;
  }

  // 删除弹窗打开时先拉清理预演（将一并移除的 Reins 条目名）。
  function openMcpPathDeleteDialog(pathId: string) {
    mcpPathDeleteDialog.open = true;
    mcpPathDeleteDialog.loading = false;
    mcpPathDeleteDialog.error = null;
    mcpPathDeleteDialog.pathId = pathId;
    mcpPathDeleteDialog.cleanupServerNames = null;
    void runWorkspaceAction({
      action: async () => {
        const preview = await previewMcpPathCleanup(pathId);
        mcpPathDeleteDialog.cleanupServerNames = preview.serverNames ?? [];
      },
      pageLock: false,
      error: "读取清理预演失败。",
      onError: (message) => {
        mcpPathDeleteDialog.error = message;
      },
    });
  }

  function closeMcpPathDeleteDialog() {
    mcpPathDeleteDialog.open = false;
    mcpPathDeleteDialog.loading = false;
    mcpPathDeleteDialog.error = null;
    mcpPathDeleteDialog.pathId = null;
    mcpPathDeleteDialog.cleanupServerNames = null;
  }

  async function handleConfirmDeleteMcpPath(cleanup: boolean) {
    const pathId = mcpPathDeleteDialog.pathId;
    if (!pathId) {
      closeMcpPathDeleteDialog();
      return;
    }

    mcpPathDeleteDialog.loading = true;
    await runWorkspaceAction({
      action: () => deleteMcpPath(pathId, cleanup),
      reload: true,
      error: "删除 MCP 路径失败。",
      onSuccess: () => closeMcpPathDeleteDialog(),
      onError: (message) => {
        mcpPathDeleteDialog.error = message;
      },
    });
    mcpPathDeleteDialog.loading = false;
  }

  return {
    mcpPathDialog,
    mcpPathDeleteDialog,
    openMcpPathCreateDialog,
    openMcpPathEditDialog,
    closeMcpPathDialog,
    clearMcpPathFormError,
    loadMcpPathPresets,
    loadMcpFormatInfos,
    handleApplyMcpPathPreset,
    handleSelectMcpPathConfigType,
    handlePickMcpPathConfigFile,
    handleSubmitMcpPath,
    openMcpPathDeleteDialog,
    closeMcpPathDeleteDialog,
    handleConfirmDeleteMcpPath,
  };
}
