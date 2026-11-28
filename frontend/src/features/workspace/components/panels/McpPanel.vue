<script setup lang="ts">
import { computed, toRefs } from "vue";
import ConfirmDialog from "@shared/ui/ConfirmDialog.vue";
import AppCard from "@shared/ui/AppCard.vue";
import AppLoadError from "@shared/ui/AppLoadError.vue";
import McpCard from "../McpCard.vue";
import McpSyncDialog from "../dialogs/McpSyncDialog.vue";
import McpCreateDialog from "../dialogs/McpCreateDialog.vue";
import McpPathCreateDialog from "../dialogs/McpPathCreateDialog.vue";
import { useMcpMutations } from "../../composables/useMcpMutations";
import { useMcpPathMutations } from "../../composables/useMcpPathMutations";
import { useMcpSync } from "../../composables/useMcpSync";
import type { WorkspaceStateContext } from "../../composables/useWorkspaceState";
import { McpSyncAction } from "../../types";
import type {
  McpInspection,
  WorkspaceConfigDocument,
  WorkspaceInspection,
} from "../../types";

type McpPanelProps = {
  configDocument: WorkspaceConfigDocument | null;
  inspection: WorkspaceInspection | null;
  runningAction: boolean;
  loadError: string | null;
  workspace: WorkspaceStateContext;
};

const props = defineProps<McpPanelProps>();

const { configDocument, inspection } = toRefs(props);
const { retryWorkspaceState } = props.workspace;

const {
  mcpCreateDialog,
  mcpDeleteDialog,
  mcpSyncConfirmDialog,
  openMcpCreateDialog,
  openMcpEditDialog,
  openMcpDeleteDialog,
  closeMcpCreateDialog,
  closeMcpSyncConfirmDialog,
  clearMcpFormError,
  handleCreateWorkspaceMcp,
  handleConfirmSyncMcpConfig,
  closeMcpDeleteDialog,
  handleConfirmDeleteMcp,
} = useMcpMutations(props.workspace);

// 矩阵同步的分发面：入口在页头按钮区，弹窗自管理勾选矩阵，聚合预演的
// 确认清单叠在同步弹窗之上。
const {
  mcpSyncDialog,
  mcpSyncDialogRef,
  mcpSyncPreviewDialog,
  openMcpSync,
  closeMcpSyncDialog,
  handleConfirmMcpSync,
  closeMcpSyncPreviewDialog,
  confirmMcpSyncExecute,
} = useMcpSync(props.workspace);

const {
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
} = useMcpPathMutations(props.workspace);

const configMcps = computed(() => configDocument.value?.config?.mcps ?? []);
const configMcpPaths = computed(() => configDocument.value?.config?.mcpPaths ?? []);

const inspectionMap = computed<Map<string, McpInspection>>(
  () => new Map((inspection.value?.mcps ?? []).map((mcp) => [mcp.name, mcp])),
);

// 聚合预演清单按动作分组：将写入（创建）在前，将移除在后。
const confirmCreateItems = computed(() =>
  mcpSyncPreviewDialog.items.filter((item) => item.action === McpSyncAction.McpSyncActionCreate),
);
const confirmRemoveItems = computed(() =>
  mcpSyncPreviewDialog.items.filter((item) => item.action === McpSyncAction.McpSyncActionRemove),
);
</script>

<template>
  <Teleport defer to="#workspace-panel-actions">
    <button
      class="primary-button"
      :disabled="runningAction"
      type="button"
      @click="openMcpSync"
    >
      同步
    </button>
    <button
      class="secondary-button"
      :disabled="runningAction"
      type="button"
      @click="openMcpCreateDialog"
    >
      新增 MCP 服务
    </button>
    <button
      class="secondary-button"
      :disabled="runningAction"
      type="button"
      @click="openMcpPathCreateDialog"
    >
      新增 MCP 路径
    </button>
  </Teleport>

  <section class="manager-stack manager-stack--stretch">
    <AppLoadError
      v-if="loadError"
      :message="loadError"
      :retrying="runningAction"
      @retry="retryWorkspaceState"
    />
    <div v-if="runningAction" class="loading-pill">正在检查状态...</div>

    <!-- 左列 = MCP 服务，右列 = MCP 路径；两列各自独立滚动。 -->
    <div class="manager-split">
      <section class="manager-split__col">
        <div class="manager-split__header">
          <span>MCP 服务 · {{ configMcps.length }} 项</span>
        </div>
        <div class="manager-scroll-region">
          <template v-if="configMcps.length">
            <div class="manager-stack">
              <McpCard
                v-for="mcp in configMcps"
                :key="mcp.name"
                :mcp="mcp"
                :inspection="inspectionMap.get(mcp.name) ?? null"
                :loading="runningAction"
                @edit-mcp="openMcpEditDialog"
                @request-delete-mcp="openMcpDeleteDialog"
              />
            </div>
          </template>
          <div v-else class="empty-state">当前还没有 mcp。</div>
        </div>
      </section>

      <!-- MCP 路径：接受 MCP 分发的命名条目（路径 + 格式 + 前缀），与技能
           路径互不约束。 -->
      <section class="manager-split__col manager-split__col--secondary">
        <div class="manager-split__header">
          <span>MCP 路径 · {{ configMcpPaths.length }} 项</span>
        </div>
        <div class="manager-scroll-region">
          <template v-if="configMcpPaths.length">
            <div class="manager-target-list">
              <AppCard
                v-for="path in configMcpPaths"
                :key="path.name"
              >
                <template #header>
                  <span class="manager-target-row__name-text">{{ path.name }}</span>
                </template>
                <template #ext>
                  <div class="manager-target-row__actions">
                    <button
                      class="secondary-button"
                      :disabled="runningAction"
                      type="button"
                      @click="openMcpPathEditDialog(path)"
                    >
                      编辑
                    </button>
                    <button
                      class="danger-button"
                      :disabled="runningAction"
                      type="button"
                      @click="openMcpPathDeleteDialog(path.name)"
                    >
                      删除
                    </button>
                  </div>
                </template>
                <div class="manager-target-row__meta manager-target-row__meta--paths">
                  <span><strong>配置</strong><span>{{ path.configPath }}</span></span>
                  <span v-if="path.configPrefix"><strong>前缀</strong><span>{{ path.configPrefix }}</span></span>
                  <span class="manager-target-row__format">{{ path.mcpFormatDescription }}</span>
                </div>
              </AppCard>
            </div>
          </template>
          <div v-else class="empty-state">当前没有 MCP 路径。从预设（按工具预填）或自定义路径开始。</div>
        </div>
      </section>
    </div>
  </section>

  <!-- --- 矩阵同步弹窗 + 聚合预演确认 --- -->

  <McpSyncDialog
    ref="mcpSyncDialogRef"
    :open="mcpSyncDialog.open"
    :config-document="configDocument ?? null"
    :inspection="inspection ?? null"
    :loading="mcpSyncDialog.loading || runningAction"
    @close="closeMcpSyncDialog"
    @confirm="handleConfirmMcpSync"
  />

  <ConfirmDialog
    :open="mcpSyncPreviewDialog.open"
    dialog-class-name="manager-import-dialog"
    eyebrow="MCP 同步"
    title="确认同步"
    title-id="mcp-sync-preview-dialog-title"
    cancel-label="取消"
    confirm-button-class-name="primary-button"
    :confirm-label="mcpSyncPreviewDialog.loading ? '同步中...' : '确认同步'"
    :loading="mcpSyncPreviewDialog.loading"
    :error="mcpSyncPreviewDialog.error"
    description="勾选集合就是期望状态：确认后按清单收敛，未列出的已装条目只做刷新。"
    @close="closeMcpSyncPreviewDialog"
    @confirm="confirmMcpSyncExecute"
  >
    <div class="manager-mcp-sync-preview">
      <section v-if="confirmCreateItems.length" class="manager-mcp-sync-preview__group">
        <h4 class="manager-mcp-sync-preview__title">将写入 {{ confirmCreateItems.length }} 项</h4>
        <div
          v-for="item in confirmCreateItems"
          :key="`${item.serverName}→${item.pathId}`"
          class="manager-mcp-sync-preview__row"
        >
          <span class="manager-mcp-sync-preview__pair">
            {{ item.serverName }} → {{ item.pathId }}
          </span>
          <!-- 构造失败的创建项不进本清单（走 failedCreates 分组），这里必然携带内容 -->
          <details class="manager-mcp-sync-preview__details">
            <summary>写入内容</summary>
            <pre class="manager-pre">{{ item.content }}</pre>
          </details>
        </div>
      </section>
      <section v-if="confirmRemoveItems.length" class="manager-mcp-sync-preview__group">
        <h4 class="manager-mcp-sync-preview__title">将移除 {{ confirmRemoveItems.length }} 项</h4>
        <div
          v-for="item in confirmRemoveItems"
          :key="`${item.serverName}→${item.pathId}`"
          class="manager-mcp-sync-preview__row"
        >
          <span class="manager-mcp-sync-preview__pair">
            {{ item.serverName }} → {{ item.pathId }}
          </span>
        </div>
      </section>
      <section
        v-if="mcpSyncPreviewDialog.failedCreates.length"
        class="manager-mcp-sync-preview__group"
      >
        <h4 class="manager-mcp-sync-preview__title">
          无法写入 {{ mcpSyncPreviewDialog.failedCreates.length }} 项（已跳过）
        </h4>
        <div
          v-for="item in mcpSyncPreviewDialog.failedCreates"
          :key="`failed-${item.serverName}→${item.pathId}`"
          class="manager-mcp-sync-preview__row"
        >
          <span class="manager-mcp-sync-preview__pair">
            {{ item.serverName }} → {{ item.pathId }}
          </span>
          <span class="manager-mcp-sync-preview__error">{{ item.detail }}</span>
        </div>
      </section>
    </div>
  </ConfirmDialog>

  <!-- --- mcp create / delete / rename sync confirm --- -->

  <McpCreateDialog
    :error="mcpCreateDialog.form.errors.form ?? null"
    :form="mcpCreateDialog.form"
    :loading="mcpCreateDialog.loading || runningAction"
    :open="mcpCreateDialog.open"
    @clear-field-error="clearMcpFormError"
    @close="closeMcpCreateDialog"
    @confirm="handleCreateWorkspaceMcp"
  />

  <ConfirmDialog
    :open="mcpDeleteDialog.open"
    dialog-class-name="manager-import-dialog"
    eyebrow="mcp"
    :title="`删除 mcp: ${mcpDeleteDialog.serverNames.join(', ')}`"
    title-id="mcp-delete-dialog-title"
    confirm-button-class-name="danger-button"
    :confirm-label="runningAction ? '删除中...' : '确认删除'"
    :loading="runningAction"
    :error="mcpDeleteDialog.error"
    description="删除后该 mcp 的配置会从工作区移除，已分发的目标配置不会自动回滚。"
    @close="closeMcpDeleteDialog"
    @confirm="handleConfirmDeleteMcp"
  />

  <ConfirmDialog
    :open="mcpSyncConfirmDialog.open"
    dialog-class-name="manager-import-dialog"
    eyebrow="mcp"
    :title="`重命名并同步: ${mcpSyncConfirmDialog.originalName} → ${mcpSyncConfirmDialog.nextName}`"
    title-id="mcp-sync-confirm-dialog-title"
    cancel-label="取消"
    confirm-button-class-name="primary-button"
    :confirm-label="mcpSyncConfirmDialog.loading ? '同步中...' : `同步 ${mcpSyncConfirmDialog.pathIds.length} 条路径`"
    :loading="mcpSyncConfirmDialog.loading"
    :error="mcpSyncConfirmDialog.error"
    :description="`该 mcp 已安装到 ${mcpSyncConfirmDialog.pathIds.length} 条 MCP 路径，重命名后需要同步更新这些路径的配置。`"
    @close="closeMcpSyncConfirmDialog"
    @confirm="handleConfirmSyncMcpConfig"
  />

  <!-- --- mcp path create / delete --- -->

  <McpPathCreateDialog
    :form="mcpPathDialog.form"
    :open="mcpPathDialog.open"
    :loading="mcpPathDialog.loading || runningAction"
    :error="mcpPathDialog.error"
    :presets="mcpPathDialog.presets"
    :presets-loading="mcpPathDialog.presetsLoading"
    :presets-error="mcpPathDialog.presetsError"
    :format-infos="mcpPathDialog.formatInfos"
    :format-infos-loading="mcpPathDialog.formatInfosLoading"
    :format-infos-error="mcpPathDialog.formatInfosError"
    :format-description="mcpPathDialog.formatDescription"
    :format-examples="mcpPathDialog.formatExamples"
    @close="closeMcpPathDialog"
    @confirm="handleSubmitMcpPath"
    @clear-field-error="clearMcpPathFormError"
    @apply-preset="handleApplyMcpPathPreset"
    @select-config-type="handleSelectMcpPathConfigType"
    @retry-load-presets="loadMcpPathPresets"
    @retry-load-format-infos="loadMcpFormatInfos"
    @pick-config-file="handlePickMcpPathConfigFile"
  />

  <ConfirmDialog
    :open="mcpPathDeleteDialog.open"
    dialog-class-name="manager-import-dialog"
    eyebrow="MCP 路径"
    :title="`删除 MCP 路径: ${mcpPathDeleteDialog.pathId ?? ''}`"
    title-id="mcp-path-delete-dialog-title"
    cancel-label="取消"
    confirm-button-class-name="danger-button"
    :confirm-label="mcpPathDeleteDialog.loading ? '删除中...' : '确认删除'"
    :loading="mcpPathDeleteDialog.loading"
    :error="mcpPathDeleteDialog.error"
    :description="mcpPathDeleteDialog.cleanupServerNames?.length ? `将一并移除写入该路径的条目：${mcpPathDeleteDialog.cleanupServerNames.join('、')}` : '该路径里没有 Reins 写入的条目，删除只移除登记。'"
    @close="closeMcpPathDeleteDialog"
    @confirm="handleConfirmDeleteMcpPath(true)"
  />
</template>
