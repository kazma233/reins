import { reactive } from "vue";
import {
  createSkillPath,
  deleteSkillPath,
  getSkillPathPresets,
  previewSkillPathCleanup,
  selectSkillPathDirectory,
  updateSkillPath,
} from "../api";
import {
  DEFAULT_SKILL_PATH_FORM,
  type FieldErrors,
  type SkillPathDeleteDialogState,
  type SkillPathFormState,
} from "../model";
import type { SkillPathPreset, SkillPathView } from "../types";
import { useWorkspaceAction } from "./useWorkspaceAction";
import type { WorkspaceStateContext } from "./useWorkspaceState";

function buildSkillPathErrors(form: SkillPathFormState): FieldErrors {
  const errors: FieldErrors = {};
  if (!form.name.trim()) {
    errors.name = "请填写条目名。";
  }
  if (!form.dir.trim()) {
    errors.dir = "请填写技能目录，或先选一个预设。";
  }
  return errors;
}

export function useSkillPathMutations(workspace: WorkspaceStateContext) {
  const { runWorkspaceAction } = useWorkspaceAction(workspace);

  const skillPathDialog = reactive<{
    open: boolean;
    loading: boolean;
    error: string | null;
    form: SkillPathFormState;
    // 内置工具预设由后端下发（目录是 env 解析后的真实值），仅创建模式使用。
    presets: SkillPathPreset[];
    presetsLoading: boolean;
    presetsError: string | null;
  }>({
    open: false,
    loading: false,
    error: null,
    form: { ...DEFAULT_SKILL_PATH_FORM, errors: {} },
    presets: [],
    presetsLoading: false,
    presetsError: null,
  });

  const skillPathDeleteDialog = reactive<SkillPathDeleteDialogState>({
    open: false,
    loading: false,
    error: null,
    pathId: null,
    cleanupPreview: null,
  });

  function openSkillPathCreateDialog(): Promise<void> {
    skillPathDialog.form = { ...DEFAULT_SKILL_PATH_FORM, errors: {} };
    skillPathDialog.error = null;
    skillPathDialog.open = true;
    return loadSkillPathPresets();
  }

  function openSkillPathEditDialog(path: SkillPathView) {
    skillPathDialog.form = {
      originalName: path.name,
      name: path.name,
      dir: path.dir ?? "",
      errors: {},
    };
    skillPathDialog.error = null;
    skillPathDialog.open = true;
  }

  function closeSkillPathDialog() {
    skillPathDialog.open = false;
    skillPathDialog.error = null;
    skillPathDialog.form = { ...DEFAULT_SKILL_PATH_FORM, errors: {} };
  }

  function clearSkillPathFormError(field: string) {
    const errors = skillPathDialog.form.errors;
    if (!errors[field]) return;
    const next = { ...errors };
    delete next[field];
    skillPathDialog.form.errors = next;
  }

  // 弹窗打开时拉取内置预设；失败常驻在预设区并给重试入口（不阻塞弹窗
  // 其余部分）。拉取不占整页忙碌态。
  async function loadSkillPathPresets() {
    skillPathDialog.presetsLoading = true;
    skillPathDialog.presetsError = null;
    await runWorkspaceAction({
      action: () => getSkillPathPresets(),
      pageLock: false,
      error: "读取预设失败。",
      onSuccess: (presets) => {
        skillPathDialog.presets = presets;
      },
      onError: (message) => {
        skillPathDialog.presetsError = message;
      },
    });
    skillPathDialog.presetsLoading = false;
  }

  // 点击预设即回填表单：条目名与目录都可回填后手改。
  function handleApplySkillPathPreset(presetName: string) {
    const preset = skillPathDialog.presets.find((item) => item.name === presetName);
    if (!preset) return;
    Object.assign(skillPathDialog.form, {
      name: preset.name,
      dir: preset.dir,
      errors: {},
    });
  }

  async function handlePickSkillPathDirectory() {
    await runWorkspaceAction({
      action: async () => {
        const currentPath = skillPathDialog.form.dir.trim() || undefined;
        const selected = await selectSkillPathDirectory(currentPath);
        if (!selected) return;
        skillPathDialog.form.dir = selected.workspaceDir;
        clearSkillPathFormError("dir");
      },
      error: "选择技能目录失败。",
      onError: (message) => {
        skillPathDialog.error = message;
      },
    });
  }

  async function handleSubmitSkillPath() {
    const form = skillPathDialog.form;
    const errors = buildSkillPathErrors(form);
    form.errors = errors;
    if (Object.keys(errors).length > 0) {
      return;
    }

    const name = form.name.trim();
    const originalName = form.originalName;
    const isEdit = originalName !== null;

    skillPathDialog.loading = true;
    await runWorkspaceAction({
      action: () =>
        isEdit
          ? updateSkillPath(originalName, name, form.dir.trim())
          : createSkillPath(name, form.dir.trim()),
      reload: true,
      error: isEdit ? "更新技能路径失败。" : "添加技能路径失败。",
      onSuccess: () => closeSkillPathDialog(),
      onError: (message) => {
        skillPathDialog.error = message;
      },
    });
    skillPathDialog.loading = false;
  }

  // 删除弹窗打开时先拉清理预演（将一并卸载的受管链接清单）；预演失败
  // 不阻塞删除本身，只是列表区显示失败原因。
  function openSkillPathDeleteDialog(pathId: string) {
    skillPathDeleteDialog.open = true;
    skillPathDeleteDialog.loading = false;
    skillPathDeleteDialog.error = null;
    skillPathDeleteDialog.pathId = pathId;
    skillPathDeleteDialog.cleanupPreview = null;
    void runWorkspaceAction({
      action: async () => {
        const preview = await previewSkillPathCleanup(pathId);
        skillPathDeleteDialog.cleanupPreview = (preview.links ?? []).map(
          (item) => `${item.skillName}（${item.destinationPath}）`,
        );
      },
      pageLock: false,
      error: "读取清理预演失败。",
      onError: (message) => {
        skillPathDeleteDialog.error = message;
      },
    });
  }

  function closeSkillPathDeleteDialog() {
    skillPathDeleteDialog.open = false;
    skillPathDeleteDialog.loading = false;
    skillPathDeleteDialog.error = null;
    skillPathDeleteDialog.pathId = null;
    skillPathDeleteDialog.cleanupPreview = null;
  }

  async function handleConfirmDeleteSkillPath(cleanup: boolean) {
    const pathId = skillPathDeleteDialog.pathId;
    if (!pathId) {
      closeSkillPathDeleteDialog();
      return;
    }

    skillPathDeleteDialog.loading = true;
    await runWorkspaceAction({
      action: () => deleteSkillPath(pathId, cleanup),
      reload: true,
      error: "删除技能路径失败。",
      onSuccess: () => closeSkillPathDeleteDialog(),
      onError: (message) => {
        skillPathDeleteDialog.error = message;
      },
    });
    skillPathDeleteDialog.loading = false;
  }

  return {
    skillPathDialog,
    skillPathDeleteDialog,
    openSkillPathCreateDialog,
    openSkillPathEditDialog,
    closeSkillPathDialog,
    clearSkillPathFormError,
    loadSkillPathPresets,
    handleApplySkillPathPreset,
    handlePickSkillPathDirectory,
    handleSubmitSkillPath,
    openSkillPathDeleteDialog,
    closeSkillPathDeleteDialog,
    handleConfirmDeleteSkillPath,
  };
}
