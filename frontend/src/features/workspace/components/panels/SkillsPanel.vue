<script setup lang="ts">
import { computed, ref, toRefs } from "vue";
import { refDebounced } from "@vueuse/core";
import SkillSourceGroup from "../SkillSourceGroup.vue";
import SkillImportDialog from "../dialogs/SkillImportDialog.vue";
import SkillPathCreateDialog from "../dialogs/SkillPathCreateDialog.vue";
import SkillSourceEditDialog from "../dialogs/SkillSourceEditDialog.vue";
import SkillSourceExportDialog from "../dialogs/SkillSourceExportDialog.vue";
import SourceSyncDialog from "../dialogs/SourceSyncDialog.vue";
import ConfirmDialog from "@shared/ui/ConfirmDialog.vue";
import AppCard from "@shared/ui/AppCard.vue";
import AppLoadError from "@shared/ui/AppLoadError.vue";
import { buildSkillLinkSourceLabels } from "../../syncAssociations";
import { useSkillDirLinks } from "../../skillDirLink";
import type { SkillSourceConfigView } from "../../types";
import type { WorkspaceStateContext } from "../../composables/useWorkspaceState";
import { useSkillImport } from "../../composables/useSkillImport";
import { useSkillPathMutations } from "../../composables/useSkillPathMutations";
import { useSkillSourceEdit } from "../../composables/useSkillSourceEdit";
import { useSourceSync } from "../../composables/useSourceSync";
import type { WorkspaceConfigDocument, WorkspaceInspection } from "../../types";

type SkillsPanelProps = {
  configDocument: WorkspaceConfigDocument | null;
  inspection: WorkspaceInspection | null;
  runningAction: boolean;
  loadError: string | null;
  workspace: WorkspaceStateContext;
};

const props = defineProps<SkillsPanelProps>();

// --- workspace state (owned by Workspace.vue, passed down as props) ---

const { configDocument, inspection, runningAction, loadError } = toRefs(props);
const { retryWorkspaceState } = props.workspace;

const configSkillSources = computed(
  () => configDocument.value?.config?.skillSources ?? [],
);
const configSkillPaths = computed(() => configDocument.value?.config?.skillPaths ?? []);
const sourceLabels = computed(() => buildSkillLinkSourceLabels(configSkillSources.value));
// 技能路径的落点归属来自 inspection（探测结果），config 只回显配置。
const skillDirLink = useSkillDirLinks(() => props.inspection);

// --- skills domain composables ---

const {
  skillImportDialog,
  openImportDialog,
  closeImportDialog,
  scanGitSkills,
  selectBatchImportFile,
  selectLocalDirectory,
  handleImportSkills,
} = useSkillImport(props.workspace);

const {
  skillSourceEditDialog,
  skillSourceDeleteDialog,
  openSkillSourceEditDialog,
  closeSkillSourceEditDialog,
  handleRefreshSkillSourceEditPreview,
  handleConfirmSkillSourceEdit,
  openSkillSourceDeleteDialog,
  closeSkillSourceDeleteDialog,
  handleConfirmDeleteSkillSource,
} = useSkillSourceEdit(props.workspace);

const {
  sourceSyncDialog,
  sourceSyncOverwriteDialog,
  handleOpenSourceSync,
  sourceSyncDialogRef,
  handleConfirmSourceSync,
  closeSourceSyncDialog,
  closeSourceSyncOverwriteDialog,
  confirmSourceSyncOverwrite,
} = useSourceSync(props.workspace);

const {
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
} = useSkillPathMutations(props.workspace);

// --- local filter state ---

// 导出弹窗是纯展示（无 API 调用），状态留在 panel 本地。
const skillSourceExportDialog = ref<{ open: boolean; sources: SkillSourceConfigView[] }>({
  open: false,
  sources: [],
});

function openSkillSourceExportDialog(sources: SkillSourceConfigView[]) {
  skillSourceExportDialog.value = { open: true, sources };
}

function closeSkillSourceExportDialog() {
  skillSourceExportDialog.value = { open: false, sources: [] };
}

const skillNameFilter = ref("");
const debouncedSkillNameFilter = refDebounced(skillNameFilter, 200);
const normalizedSkillNameFilter = computed(() =>
  debouncedSkillNameFilter.value.trim().toLocaleLowerCase(),
);

const visibleSources = computed(() => {
  if (!normalizedSkillNameFilter.value) return configSkillSources.value;
  return configSkillSources.value.filter(
    (source) =>
      source.label.toLocaleLowerCase().includes(normalizedSkillNameFilter.value) ||
      (source.description ?? "").toLocaleLowerCase().includes(normalizedSkillNameFilter.value),
  );
});

const localSources = computed(() =>
  visibleSources.value.filter((source) => source.type === "local"),
);
const remoteSources = computed(() =>
  visibleSources.value.filter((source) => source.type === "git"),
);
</script>

<template>
  <Teleport defer to="#workspace-panel-actions">
    <input
      id="manager-skill-name-filter"
      v-model="skillNameFilter"
      class="manager-skills-toolbar__search-input"
      placeholder="按来源搜索..."
      type="text"
    />
    <button
      class="primary-button"
      :disabled="runningAction"
      type="button"
      @click="openImportDialog"
    >
      新增来源
    </button>
    <button
      class="secondary-button"
      :disabled="runningAction"
      type="button"
      @click="openSkillPathCreateDialog"
    >
      新增技能路径
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

    <!-- 左列 = 技能源，右列 = 技能路径；两列各自独立滚动。 -->
    <div class="manager-split">
      <section class="manager-split__col">
        <div class="manager-split__header">
          <span>技能来源 · {{ visibleSources.length }} 项</span>
        </div>
        <div class="manager-scroll-region">
          <template v-if="visibleSources.length">
            <div class="manager-skill-source-groups">
              <SkillSourceGroup
                heading="本地 Skills"
                heading-id="local-skills-heading"
                :loading="runningAction"
                :sources="localSources"
                @edit="openSkillSourceEditDialog"
                @delete="openSkillSourceDeleteDialog"
                @sync="handleOpenSourceSync"
              />
              <SkillSourceGroup
                heading="远端 Skills"
                heading-id="remote-skills-heading"
                :loading="runningAction"
                :sources="remoteSources"
                exportable
                @edit="openSkillSourceEditDialog"
                @delete="openSkillSourceDeleteDialog"
                @sync="handleOpenSourceSync"
                @export="openSkillSourceExportDialog"
              />
            </div>
          </template>
          <div v-else class="empty-state">当前没有匹配的来源。</div>
        </div>
      </section>

      <!-- 技能路径：接受 skills 分发的命名目录条目，与 MCP 路径互不约束。 -->
      <section class="manager-split__col manager-split__col--secondary">
        <div class="manager-split__header">
          <span>技能路径 · {{ configSkillPaths.length }} 项</span>
        </div>
        <div class="manager-scroll-region">
          <template v-if="configSkillPaths.length">
            <div class="manager-target-list">
              <AppCard
                v-for="path in configSkillPaths"
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
                      @click="openSkillPathEditDialog(path)"
                    >
                      编辑
                    </button>
                    <button
                      class="danger-button"
                      :disabled="runningAction"
                      type="button"
                      @click="openSkillPathDeleteDialog(path.name)"
                    >
                      删除
                    </button>
                  </div>
                </template>
                <div class="manager-target-row__meta manager-target-row__meta--paths">
                  <span>
                    <strong>目录</strong>
                    <span>{{ path.dir }}</span>
                    <!-- 目录不在盘上：可能是待安装，也可能是路径写错，中性提示。 -->
                    <span
                      v-if="skillDirLink(path.name)?.exists === false"
                      class="manager-target-row__pill pill--note"
                    >目录不存在</span>
                  </span>
                  <!-- 目录是软链接：这个条目不拥有它，内容在别处。 -->
                  <span v-if="skillDirLink(path.name)?.place" class="manager-target-row__link">
                    <span class="manager-target-row__pill pill--info">软链接</span>
                    <span class="manager-target-row__place">{{ skillDirLink(path.name)?.place }}</span>
                  </span>
                </div>
              </AppCard>
            </div>
          </template>
          <div v-else class="empty-state">当前没有技能路径。从预设（按工具预填）或自定义任意目录开始。</div>
        </div>
      </section>
    </div>
  </section>

    <!-- --- skill import --- -->

    <SkillImportDialog
      :dialog-state="skillImportDialog"
      :error="skillImportDialog.preview.previewError"
      :loading="runningAction"
      @close="closeImportDialog"
      @confirm="handleImportSkills"
      @discover-git-skills="scanGitSkills"
      @pick-batch-import-file="selectBatchImportFile"
      @pick-local-skill-directory="selectLocalDirectory"
    />

    <!-- --- skill source edit / delete --- -->

    <SkillSourceEditDialog
      :error="skillSourceEditDialog.preview.previewError"
      :loading="runningAction"
      :state="skillSourceEditDialog"
      @close="closeSkillSourceEditDialog"
      @confirm="handleConfirmSkillSourceEdit"
      @refresh-preview="handleRefreshSkillSourceEditPreview"
    />

    <SkillSourceExportDialog
      :open="skillSourceExportDialog.open"
      :sources="skillSourceExportDialog.sources"
      @close="closeSkillSourceExportDialog"
    />

    <ConfirmDialog
      :open="skillSourceDeleteDialog.open"
      dialog-class-name="manager-import-dialog"
      eyebrow="Source"
      :title="`删除来源: ${skillSourceDeleteDialog.source?.label ?? ''}`"
      title-id="skill-source-delete-dialog-title"
      confirm-button-class-name="danger-button"
      :confirm-label="runningAction ? '删除中...' : '确认删除'"
      :loading="runningAction"
      :error="skillSourceDeleteDialog.error"
      @close="closeSkillSourceDeleteDialog"
      @confirm="handleConfirmDeleteSkillSource"
    >
      <p class="manager-skill-description">
        删除后会移除技能路径中指向该来源的软链接，但不会删除来源目录中的原始 skills。
      </p>
    </ConfirmDialog>

    <!-- --- source sync --- -->

    <SourceSyncDialog
      ref="sourceSyncDialogRef"
      :loading="sourceSyncDialog.loading"
      :open="sourceSyncDialog.open"
      :source="sourceSyncDialog.source"
      :source-labels="sourceLabels"
      :workspace="props.workspace"
      @close="closeSourceSyncDialog"
      @confirm="handleConfirmSourceSync"
    />

    <!-- --- skill path create / delete --- -->

    <SkillPathCreateDialog
      :form="skillPathDialog.form"
      :open="skillPathDialog.open"
      :loading="skillPathDialog.loading || runningAction"
      :error="skillPathDialog.error"
      :presets="skillPathDialog.presets"
      :presets-loading="skillPathDialog.presetsLoading"
      :presets-error="skillPathDialog.presetsError"
      @close="closeSkillPathDialog"
      @confirm="handleSubmitSkillPath"
      @clear-field-error="clearSkillPathFormError"
      @apply-preset="handleApplySkillPathPreset"
      @retry-load-presets="loadSkillPathPresets"
      @pick-skill-directory="handlePickSkillPathDirectory"
    />

    <ConfirmDialog
      :open="skillPathDeleteDialog.open"
      dialog-class-name="manager-import-dialog"
      eyebrow="技能路径"
      :title="`删除技能路径: ${skillPathDeleteDialog.pathId ?? ''}`"
      title-id="skill-path-delete-dialog-title"
      cancel-label="取消"
      confirm-button-class-name="danger-button"
      :confirm-label="skillPathDeleteDialog.loading ? '删除中...' : '确认删除'"
      :loading="skillPathDeleteDialog.loading"
      :error="skillPathDeleteDialog.error"
      :description="skillPathDeleteDialog.cleanupPreview?.length ? `将一并卸载写入该路径的 ${skillPathDeleteDialog.cleanupPreview.length} 条受管链接` : '该路径里没有受管链接，删除只移除登记。'"
      @close="closeSkillPathDeleteDialog"
      @confirm="handleConfirmDeleteSkillPath(true)"
    >
      <div
        v-if="skillPathDeleteDialog.cleanupPreview?.length"
        class="manager-delete-confirm-list"
      >
        <div
          v-for="item in skillPathDeleteDialog.cleanupPreview"
          :key="item"
          class="manager-import-skill-row manager-import-skill-row--readonly"
        >
          <p class="manager-skill-description">{{ item }}</p>
        </div>
      </div>
    </ConfirmDialog>

    <ConfirmDialog
      :open="sourceSyncOverwriteDialog.open"
      dialog-class-name="manager-import-dialog"
      eyebrow="Sync"
      title="覆盖已有软链接"
      title-id="source-sync-overwrite-dialog-title"
      cancel-label="取消"
      confirm-button-class-name="danger-button"
      :confirm-label="sourceSyncOverwriteDialog.loading ? '覆盖中...' : '覆盖并同步'"
      :loading="sourceSyncOverwriteDialog.loading"
      :error="sourceSyncOverwriteDialog.error"
      @close="closeSourceSyncOverwriteDialog"
      @confirm="confirmSourceSyncOverwrite"
    >
      <p class="manager-skill-description">
        以下技能路径已存在软链接，覆盖会先移除旧链接再写入新链接。
      </p>
      <div class="manager-delete-confirm-list">
        <div
          v-for="(conflict, index) in sourceSyncOverwriteDialog.conflicts"
          :key="`${conflict.skillName}-${conflict.pathId}-${index}`"
          class="manager-import-skill-row manager-import-skill-row--readonly"
        >
          <div class="manager-skill-headline">
            <div class="manager-skill-headline__meta">
              <span class="manager-skill-name">{{ conflict.skillName }}</span>
              <span class="manager-skill-updated">
                {{ conflict.pathId }}
              </span>
            </div>
            <span class="pill danger-pill">{{ conflict.existingKind }}</span>
          </div>
          <p class="manager-skill-description">{{ conflict.detail }}</p>
        </div>
      </div>
    </ConfirmDialog>
</template>
