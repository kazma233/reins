import { reactive, ref } from "vue";
import { previewMcpSync, syncMcpsToPaths } from "../api";
import { excludeCells } from "../mcpSync";
import type { McpSyncCell, McpSyncPreviewItem } from "../types";
import { extractErrorMessage } from "@shared/lib/errors";
import type { WorkspaceStateContext } from "./useWorkspaceState";

// MCP 矩阵同步的编排（对齐 useSourceSync 的形态）：McpSyncDialog 自管理
// 勾选矩阵，这里只管弹窗开关、聚合预演确认与执行；结果回写到弹窗底部
// 按钮旁，面板列表由后台刷新自行更新（决策记录
// 2026-11-28-mcp-matrix-sync.md 第 3 条）。
export function useMcpSync(workspace: WorkspaceStateContext) {
  const { reloadWorkspaceState } = workspace;

  const mcpSyncDialogRef = ref<{ reportResult: (result: { text: string; failed: boolean }) => void } | null>(
    null,
  );

  const mcpSyncDialog = reactive<{
    open: boolean;
    loading: boolean;
  }>({
    open: false,
    loading: false,
  });

  // 聚合预演的确认弹窗：items 为空（纯刷新）时不弹，直接执行。
  // failedCreates 是条目构造失败的创建项——只告知，不参与执行（决策记录
  // 2026-11-28-mcp-matrix-sync.md 第 3 条）。
  const mcpSyncPreviewDialog = reactive<{
    open: boolean;
    loading: boolean;
    error: string | null;
    items: McpSyncPreviewItem[];
    failedCreates: McpSyncPreviewItem[];
    pending: McpSyncCell[] | null;
  }>({
    open: false,
    loading: false,
    error: null,
    items: [],
    failedCreates: [],
    pending: null,
  });

  function openMcpSync() {
    mcpSyncDialog.open = true;
    mcpSyncDialog.loading = false;
  }

  function closeMcpSyncDialog() {
    mcpSyncDialog.open = false;
    mcpSyncDialog.loading = false;
  }

  function closeMcpSyncPreviewDialog() {
    mcpSyncPreviewDialog.open = false;
    mcpSyncPreviewDialog.loading = false;
    mcpSyncPreviewDialog.error = null;
    mcpSyncPreviewDialog.items = [];
    mcpSyncPreviewDialog.failedCreates = [];
    mcpSyncPreviewDialog.pending = null;
  }

  function reportResult(result: { text: string; failed: boolean }) {
    mcpSyncDialogRef.value?.reportResult(result);
  }

  function describeFailedCreates(items: McpSyncPreviewItem[]): string[] {
    if (items.length === 0) return [];
    return [
      `无法写入 ${items.length} 项（已跳过）：${items
        .map((item) => `${item.serverName} → ${item.pathId}`)
        .join("、")}`,
    ];
  }

  function describeSyncResult(
    result: {
      applied?: { serverName: string; pathId: string }[] | null;
      removed?: { serverName: string; pathId: string }[] | null;
      failed?: { serverName: string; pathId: string }[] | null;
      warnings?: string[] | null;
    },
    extraLines: string[] = [],
  ): { text: string; failed: boolean } {
    const applied = result.applied ?? [];
    const removed = result.removed ?? [];
    const failed = result.failed ?? [];
    const warnings = result.warnings ?? [];
    const lines = [`同步完成：写入 ${applied.length} 项，移除 ${removed.length} 项。`];
    if (failed.length > 0) {
      lines.push(
        `失败 ${failed.length} 项：${failed.map((item) => `${item.serverName} → ${item.pathId}`).join("、")}`,
      );
    }
    lines.push(...warnings);
    lines.push(...extraLines);
    return {
      text: lines.join("\n"),
      failed: failed.length > 0 || warnings.length > 0 || extraLines.length > 0,
    };
  }

  async function runMcpSync(cells: McpSyncCell[], skippedCreates: McpSyncPreviewItem[] = []) {
    const result = await syncMcpsToPaths({ cells });
    reportResult(describeSyncResult(result, describeFailedCreates(skippedCreates)));
    // 确认弹窗只叠在同步弹窗之上，同步结束就该收起来；同步弹窗保持打开，
    // 勾选矩阵就地反映刷新后的状态。
    closeMcpSyncPreviewDialog();
    await reloadWorkspaceState({ background: true });
  }

  async function handleConfirmMcpSync(cells: McpSyncCell[]) {
    mcpSyncDialog.loading = true;
    try {
      const preview = await previewMcpSync({ cells });
      const items = preview.items ?? [];
      const failedCreates = preview.failedCreates ?? [];
      // 构造失败的创建格从执行集合剔除：确认清单里的就是将要执行的（§3）。
      const executable = excludeCells(
        cells,
        failedCreates.map((item) => ({ serverName: item.serverName, pathId: item.pathId })),
      );
      if (items.length === 0) {
        await runMcpSync(executable, failedCreates);
        return;
      }
      mcpSyncPreviewDialog.open = true;
      mcpSyncPreviewDialog.loading = false;
      mcpSyncPreviewDialog.error = null;
      mcpSyncPreviewDialog.items = items;
      mcpSyncPreviewDialog.failedCreates = failedCreates;
      mcpSyncPreviewDialog.pending = executable;
    } catch (error) {
      reportResult({ text: extractErrorMessage(error, "预演同步失败。"), failed: true });
    } finally {
      // 弹窗不关，预演结束后必须解开按钮，不然用户接着操作会被锁住。
      mcpSyncDialog.loading = false;
    }
  }

  async function confirmMcpSyncExecute() {
    const pending = mcpSyncPreviewDialog.pending;
    if (!pending) {
      closeMcpSyncPreviewDialog();
      return;
    }

    mcpSyncPreviewDialog.loading = true;
    mcpSyncDialog.loading = true;
    try {
      await runMcpSync(pending, mcpSyncPreviewDialog.failedCreates);
    } catch (error) {
      mcpSyncPreviewDialog.error = extractErrorMessage(error, "同步失败。");
    } finally {
      mcpSyncPreviewDialog.loading = false;
      mcpSyncDialog.loading = false;
    }
  }

  return {
    mcpSyncDialog,
    mcpSyncDialogRef,
    mcpSyncPreviewDialog,
    openMcpSync,
    closeMcpSyncDialog,
    handleConfirmMcpSync,
    closeMcpSyncPreviewDialog,
    confirmMcpSyncExecute,
  };
}
