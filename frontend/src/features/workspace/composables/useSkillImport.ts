import { reactive } from "vue";
import {
  importBatchGitSkills,
  importDiscoveredSkills,
  selectLocalSkillSourceDirectory,
  selectSkillBatchImportFile,
} from "../api";
import {
  createSkillImportDialogState,
  parseCommaSeparatedList,
  type SkillImportDialogState,
} from "../model";
import { useSkillPreview, type SkillSourcePreviewInput } from "./useSkillPreview";
import { useWorkspaceAction } from "./useWorkspaceAction";
import type { SkillSourceKind } from "../types";
import type { WorkspaceStateContext } from "./useWorkspaceState";

// batch-git 不产生 discovery 预览，调用方保证只在 git/local 分支传入对应 kind。
function buildImportSourcePreviewInput(
  dialog: SkillImportDialogState,
  sourceType: SkillSourceKind,
): SkillSourcePreviewInput {
  return {
    sourceType,
    repo: dialog.repo,
    rootPath: dialog.rootPath,
    ref: dialog.ref,
  };
}

export function useSkillImport(workspace: WorkspaceStateContext) {
  const { runWorkspaceAction } = useWorkspaceAction(workspace);

  const skillImportDialog = reactive<SkillImportDialogState>(createSkillImportDialogState());
  const { discoverAndFilterSkills, invalidatePreviewRequests, clearPreview, failPreview } =
    useSkillPreview(skillImportDialog);

  function openImportDialog() {
    Object.assign(skillImportDialog, createSkillImportDialogState(skillImportDialog.sourceKind));
    skillImportDialog.open = true;
  }

  function closeImportDialog() {
    invalidatePreviewRequests();
    Object.assign(skillImportDialog, createSkillImportDialogState(skillImportDialog.sourceKind));
  }

  async function scanGitSkills() {
    const source = buildImportSourcePreviewInput(skillImportDialog, "git");
    // Reset preview so the loading state is visible while discovery runs.
    clearPreview();

    await runWorkspaceAction({
      action: () =>
        discoverAndFilterSkills(
          source,
          skillImportDialog.preview.includeNamePatternsText,
          skillImportDialog.preview.includePathPatternsText,
        ),
      error: "发现 git skills 失败。",
      onError: (message) => failPreview(message, "发现 git skills 失败。"),
    });
  }

  // 选择批量导入 JSON 文件：内容直接填入编辑框，选后仍可手动微调再导入。
  async function selectBatchImportFile() {
    await runWorkspaceAction({
      action: async () => {
        const selected = await selectSkillBatchImportFile();
        if (!selected) return;

        skillImportDialog.batchFilePath = selected.path;
        skillImportDialog.batchJsonText = selected.content;
        skillImportDialog.batchResult = null;
      },
      error: "选择批量导入文件失败。",
      onError: (message) => {
        skillImportDialog.preview.previewError = message;
      },
    });
  }

  async function selectLocalDirectory() {
    await runWorkspaceAction({
      action: async () => {
        const selected = await selectLocalSkillSourceDirectory(skillImportDialog.rootPath);
        if (!selected) return;

        skillImportDialog.sourceKind = "local";
        skillImportDialog.rootPath = selected.workspaceDir;
        clearPreview();

        await discoverAndFilterSkills(
          buildImportSourcePreviewInput(skillImportDialog, "local"),
          skillImportDialog.preview.includeNamePatternsText,
          skillImportDialog.preview.includePathPatternsText,
        );
      },
      error: "选择本地目录失败。",
      // 取消选择也会走到这里（selected 为空直接返回），只有目录本身读取失败
      // 才需要预览区域提示；两者都是「没有结果」，用同一处常驻文案表达。
      onError: (message) => failPreview(message, "选择本地目录失败。"),
    });
  }

  async function handleImportSkills() {
    if (skillImportDialog.sourceKind === "batch-git") {
      await runWorkspaceAction({
        action: () => importBatchGitSkills(skillImportDialog.batchJsonText),
        reload: true,
        error: "导入 skills 失败。",
        onSuccess: (result) => {
          // Batch results stay in the dialog so the user can review failures.
          skillImportDialog.loading = false;
          skillImportDialog.batchResult = result;
        },
        onError: (message) => {
          skillImportDialog.loading = false;
          skillImportDialog.preview.previewError = message;
        },
      });
      return;
    }

    await runWorkspaceAction({
      action: async () => {
        const discoveryId = skillImportDialog.preview.discovery?.discoveryId ?? "";
        const description = skillImportDialog.description.trim();
        const namePatterns = parseCommaSeparatedList(skillImportDialog.preview.includeNamePatternsText);
        const pathPatterns = parseCommaSeparatedList(skillImportDialog.preview.includePathPatternsText);

        if (skillImportDialog.sourceKind === "local" && !skillImportDialog.rootPath.trim()) {
          throw new Error("缺少本地来源目录。");
        }

        return importDiscoveredSkills(discoveryId, description, namePatterns, pathPatterns);
      },
      reload: true,
      error: "导入 skills 失败。",
      onSuccess: () => closeImportDialog(),
      onError: (message) => {
        skillImportDialog.preview.previewError = message;
      },
    });
  }

  return {
    skillImportDialog,
    openImportDialog,
    closeImportDialog,
    scanGitSkills,
    selectBatchImportFile,
    selectLocalDirectory,
    handleImportSkills,
  };
}
