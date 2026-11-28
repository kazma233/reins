import { reactive } from "vue";
import {
  createWorkspaceMcp,
  deleteWorkspaceMcp,
  updateWorkspaceMcp,
} from "../api";
import { type McpDeleteDialogState } from "../model";
import {
  buildMcpErrors,
  buildMcpPayload,
  emptyMcpForm,
  formFromMcp,
  type McpFormPayload,
  type McpFormState,
} from "../mcpForm";
import { McpPathState, type McpConfigView } from "../types";
import { useWorkspaceAction } from "./useWorkspaceAction";
import type { WorkspaceStateContext } from "./useWorkspaceState";

type McpSyncConfirmDialogState = {
  open: boolean;
  loading: boolean;
  error: string | null;
  originalName: string;
  nextName: string;
  payload: McpFormPayload | null;
  pathIds: string[];
};

const DEFAULT_MCP_SYNC_CONFIRM_DIALOG: McpSyncConfirmDialogState = {
  open: false,
  loading: false,
  error: null,
  originalName: "",
  nextName: "",
  payload: null,
  pathIds: [],
};

// MCP 服务的登记面（创建/编辑/删除）。分发统一走 useMcpSync 的矩阵同步；
// 改名时已装路径的重写由后端在锁内一次完成（决策记录
// 2026-11-28-mcp-matrix-sync.md 第 5 条），前端只弹确认。
export function useMcpMutations(workspace: WorkspaceStateContext) {
  const { inspection } = workspace;
  const { runWorkspaceAction } = useWorkspaceAction(workspace);

  const mcpCreateDialog = reactive<{
    open: boolean;
    loading: boolean;
    form: McpFormState;
  }>({
    open: false,
    loading: false,
    form: emptyMcpForm(),
  });

  const mcpDeleteDialog = reactive<McpDeleteDialogState>({
    open: false,
    loading: false,
    error: null,
    serverNames: [],
  });

  const mcpSyncConfirmDialog = reactive<McpSyncConfirmDialogState>({
    ...DEFAULT_MCP_SYNC_CONFIRM_DIALOG,
  });

  function getInstalledMcpPathIds(serverName: string): string[] {
    return Array.from(
      new Set(
        (inspection.value?.mcps ?? [])
          .find((mcp) => mcp.name === serverName)
          ?.paths?.filter((path) => path.state === McpPathState.PathStatePresent)
          .map((path) => path.pathId) ?? [],
      ),
    );
  }

  function openMcpCreateDialog() {
    mcpCreateDialog.form = emptyMcpForm();
    mcpCreateDialog.open = true;
  }

  function openMcpEditDialog(mcp: McpConfigView) {
    mcpCreateDialog.form = formFromMcp(mcp);
    mcpCreateDialog.open = true;
  }

  function clearMcpFormError(field: string) {
    if (!mcpCreateDialog.form.errors[field]) return;
    const next = { ...mcpCreateDialog.form.errors };
    delete next[field];
    mcpCreateDialog.form.errors = next;
  }

  function closeMcpCreateDialog() {
    Object.assign(mcpSyncConfirmDialog, DEFAULT_MCP_SYNC_CONFIRM_DIALOG);
    mcpCreateDialog.open = false;
    mcpCreateDialog.form = emptyMcpForm();
  }

  function closeMcpSyncConfirmDialog() {
    Object.assign(mcpSyncConfirmDialog, DEFAULT_MCP_SYNC_CONFIRM_DIALOG);
  }

  // 校验与组装都走 mcpForm：表单规则只有一处，行级校验有直接测试。
  function buildMcpPayloadOrErrors(): McpFormPayload | null {
    const form = mcpCreateDialog.form;
    const payload = buildMcpPayload(form);
    form.errors = buildMcpErrors(form);
    return payload;
  }

  // 用户能看到所有待改字段而不是修一个才发现下一个。
  async function handleCreateWorkspaceMcp() {
    const form = mcpCreateDialog.form;
    const payload = buildMcpPayloadOrErrors();
    if (!payload) {
      return;
    }
    const name = payload.name;

    if (form.originalName) {
      // 「重命名并同步」只在名字真的变了才有意义：只是改连接参数时名字没变，
      // 弹一个重命名确认会误导用户，直接保存即可（内容变更由同步弹窗刷新）。
      const isRename = payload.name !== form.originalName;
      const pathIds = isRename ? getInstalledMcpPathIds(form.originalName) : [];
      if (pathIds.length) {
        mcpSyncConfirmDialog.open = true;
        mcpSyncConfirmDialog.loading = false;
        mcpSyncConfirmDialog.error = null;
        mcpSyncConfirmDialog.originalName = form.originalName;
        mcpSyncConfirmDialog.nextName = payload.name;
        mcpSyncConfirmDialog.payload = payload;
        mcpSyncConfirmDialog.pathIds = pathIds;
        return;
      }
    }

    const isEdit = form.originalName !== null;
    mcpCreateDialog.loading = true;
    await runWorkspaceAction({
      action: () =>
        form.originalName
          ? updateWorkspaceMcp(form.originalName, payload)
          : createWorkspaceMcp(payload),
      reload: true,
      error: isEdit ? "更新 mcp 失败。" : "添加 mcp 失败。",
      onSuccess: () => closeMcpCreateDialog(),
      onError: (message) => {
        mcpCreateDialog.form.errors = { form: message };
      },
    });
    mcpCreateDialog.loading = false;
  }

  // 改名确认：登记更新与已装路径重写是后端一条命令，这里不再逐路径串行调。
  async function handleConfirmSyncMcpConfig() {
    const { originalName, payload } = mcpSyncConfirmDialog;
    if (!originalName || !payload) return;

    mcpSyncConfirmDialog.loading = true;
    mcpCreateDialog.loading = true;
    await runWorkspaceAction({
      action: () => updateWorkspaceMcp(originalName, payload),
      reload: true,
      error: "更新 mcp 并同步已安装路径失败。",
      onSuccess: () => {
        closeMcpSyncConfirmDialog();
        closeMcpCreateDialog();
      },
      onError: (message) => {
        mcpSyncConfirmDialog.error = message;
      },
    });
    mcpSyncConfirmDialog.loading = false;
    mcpCreateDialog.loading = false;
  }

  function openMcpDeleteDialog(serverNames: string[]) {
    mcpDeleteDialog.open = true;
    mcpDeleteDialog.loading = false;
    mcpDeleteDialog.error = null;
    mcpDeleteDialog.serverNames = serverNames;
  }

  function closeMcpDeleteDialog() {
    mcpDeleteDialog.open = false;
    mcpDeleteDialog.loading = false;
    mcpDeleteDialog.error = null;
    mcpDeleteDialog.serverNames = [];
  }

  async function handleConfirmDeleteMcp() {
    const serverName = mcpDeleteDialog.serverNames[0];
    if (!serverName) {
      closeMcpDeleteDialog();
      return;
    }

    mcpDeleteDialog.loading = true;
    await runWorkspaceAction({
      action: () => deleteWorkspaceMcp(serverName),
      reload: true,
      error: "删除 mcp 失败。",
      onSuccess: () => closeMcpDeleteDialog(),
      onError: (message) => {
        mcpDeleteDialog.error = message;
      },
    });
    mcpDeleteDialog.loading = false;
  }

  return {
    mcpCreateDialog,
    mcpDeleteDialog,
    mcpSyncConfirmDialog,
    openMcpCreateDialog,
    openMcpEditDialog,
    closeMcpCreateDialog,
    closeMcpSyncConfirmDialog,
    clearMcpFormError,
    handleCreateWorkspaceMcp,
    handleConfirmSyncMcpConfig,
    openMcpDeleteDialog,
    closeMcpDeleteDialog,
    handleConfirmDeleteMcp,
  };
}
