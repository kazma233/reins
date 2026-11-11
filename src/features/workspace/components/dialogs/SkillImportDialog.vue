<script setup lang="ts">
import { computed } from "vue";
import DialogShell from "@shared/ui/DialogShell.vue";
import AppFieldError from "@shared/ui/AppFieldError.vue";
import BatchGitImportResultList from "../BatchGitImportResultList.vue";
import SkillDiscoveryPreview from "../SkillDiscoveryPreview.vue";
import type { SkillDiscoveryResult } from "../../types";
import type { SkillImportDialogState } from "../../model";

type SkillImportDialogProps = {
  dialogState: SkillImportDialogState;
  loading: boolean;
  error: string | null;
};

const props = defineProps<SkillImportDialogProps>();

defineEmits<{
  close: [];
  confirm: [];
  discoverGitSkills: [];
  pickBatchImportFile: [];
  pickLocalSkillDirectory: [];
}>();

const BATCH_GIT_SKILL_IMPORT_PLACEHOLDER = JSON.stringify(
  [
    {
      repo: "https://github.com/anthropics/skills.git",
      ref: "main",
      description: "Anthropic 官方技能库",
      include_name_patterns: ["agent-*"],
      include_path_patterns: ["python/*"],
    },
  ],
  null,
  2,
);

function canConfirmSkillImport(state: SkillImportDialogState): boolean {
  if (state.sourceKind === "batch-git") return state.batchJsonText.trim().length > 0;
  return Boolean(state.preview.discovery?.skills.length);
}

const canConfirm = computed(() => canConfirmSkillImport(props.dialogState));

function handleBatchJsonInput(event: Event) {
  const value = (event.target as HTMLTextAreaElement).value;
  props.dialogState.batchJsonText = value;
  props.dialogState.batchResult = null;
}

// 路径是辅助信息，行内只显示文件名，完整路径由 title 兜底。
const batchFileName = computed(() => {
  const path = props.dialogState.batchFilePath;
  if (!path) return null;
  const segments = path.split(/[/\\]/);
  return segments[segments.length - 1] || path;
});

function handleRepoInput(event: Event) {
  props.dialogState.repo = (event.target as HTMLInputElement).value;
}

function handleRefInput(event: Event) {
  props.dialogState.ref = (event.target as HTMLInputElement).value;
}

function handleDescriptionInput(event: Event) {
  props.dialogState.description = (event.target as HTMLInputElement).value;
}

function handleNamePatternsInput(value: string) {
  const preview = props.dialogState.preview;
  preview.previewLoading = Boolean(preview.discovery);
  preview.includeNamePatternsText = value;
}

function handlePathPatternsInput(value: string) {
  const preview = props.dialogState.preview;
  preview.previewLoading = Boolean(preview.discovery);
  preview.includePathPatternsText = value;
}

function renderDiscoverySummary(discovery: SkillDiscoveryResult | null, loading = false) {
  if (!discovery || loading) return null;
  const includeCount = discovery.includeNamePatterns.length + discovery.includePathPatterns.length;
  const excludedCount = discovery.excludedSkills?.length ?? 0;
  return `将导入 ${discovery.skills.length} 个 skills${includeCount ? ` · 匹配 ${includeCount} 项` : ""}${excludedCount ? ` · 排除 ${excludedCount} 项` : ""}`;
}

const summaryText = computed(() =>
  renderDiscoverySummary(props.dialogState.preview.discovery, props.dialogState.preview.previewLoading),
);

// 本地来源没有「扫描」按钮，读取状态与结果摘要就近挂在「选择目录」右侧，
// 与 git 分支「扫描来源 + 摘要」的动作行结构对齐。
const localStatusText = computed(() =>
  props.dialogState.preview.previewLoading ? "正在读取来源..." : summaryText.value,
);
</script>

<template>
  <DialogShell
    :open="dialogState.open"
    dialog-class-name="manager-import-dialog manager-import-dialog--tall"
    title="导入 skills"
    title-id="skill-import-dialog-title"
    :close-disabled="loading"
    @close="$emit('close')"
  >
    <template #actions>
      <button
        class="primary-button"
        :disabled="loading || !canConfirm"
        type="button"
        @click="$emit('confirm')"
      >
        导入
      </button>
    </template>

    <div class="manager-import-shell manager-import-shell--compact">
      <AppFieldError :message="error" />

      <div class="manager-segmented manager-segmented--triple" role="group" aria-label="导入来源">
        <button
          :class="`manager-segmented__button${dialogState.sourceKind === 'git' ? ' is-active' : ''}`"
          type="button"
          @click="dialogState.sourceKind = 'git'"
        >
          Git 仓库
        </button>
        <button
          :class="`manager-segmented__button${dialogState.sourceKind === 'local' ? ' is-active' : ''}`"
          type="button"
          @click="dialogState.sourceKind = 'local'"
        >
          本地目录
        </button>
        <button
          :class="`manager-segmented__button${dialogState.sourceKind === 'batch-git' ? ' is-active' : ''}`"
          type="button"
          @click="dialogState.sourceKind = 'batch-git'"
        >
          批量 Git
        </button>
      </div>

      <template v-if="dialogState.sourceKind === 'batch-git'">
        <label class="manager-field">
          <span>JSON 内容</span>
          <textarea
            class="manager-editor manager-textarea manager-batch-import-editor"
            :placeholder="BATCH_GIT_SKILL_IMPORT_PLACEHOLDER"
            :value="dialogState.batchJsonText"
            @input="handleBatchJsonInput"
          />
        </label>
        <div class="manager-actions">
          <button
            class="secondary-button"
            :disabled="loading"
            type="button"
            @click="$emit('pickBatchImportFile')"
          >
            选择 JSON 文件
          </button>
          <span
            v-if="batchFileName"
            class="manager-inline-hint"
            :title="dialogState.batchFilePath ?? undefined"
          >
            {{ batchFileName }}
          </span>
        </div>
      </template>

      <template v-else>
        <label class="manager-field">
          <span>描述</span>
          <input
            placeholder="一句话说明这个来源的用途，会显示在列表标题"
            :value="dialogState.description"
            type="text"
            @input="handleDescriptionInput"
          />
        </label>

        <div v-if="dialogState.sourceKind === 'git'" class="manager-import-row manager-import-row--git-source">
          <label class="manager-field manager-import-row__field manager-import-row__field--repo">
            <span>Git 仓库</span>
            <input
              placeholder="https://github.com/anthropics/skills.git"
              :value="dialogState.repo"
              type="text"
              @input="handleRepoInput"
            />
          </label>
          <label class="manager-field manager-import-row__field manager-import-row__field--ref">
            <span>分支</span>
            <input
              :value="dialogState.ref"
              type="text"
              @input="handleRefInput"
            />
          </label>
        </div>

        <div v-else class="manager-import-row">
          <label class="manager-field manager-import-row__field">
            <span>导入目录</span>
            <input readonly :value="dialogState.rootPath" type="text" />
          </label>
          <div class="manager-actions manager-import-row__actions">
            <button
              class="secondary-button"
              :disabled="loading"
              type="button"
              @click="$emit('pickLocalSkillDirectory')"
            >
              选择目录
            </button>
            <span v-if="localStatusText" class="manager-inline-hint">{{ localStatusText }}</span>
          </div>
        </div>

        <div class="manager-import-row manager-import-row--include-patterns">
          <label class="manager-field manager-import-row__field">
            <span>匹配名称</span>
            <input
              placeholder="按技能名称匹配，如 agent-*,draft-*"
              :value="dialogState.preview.includeNamePatternsText"
              type="text"
              @input="(event: Event) => handleNamePatternsInput((event.target as HTMLInputElement).value)"
            />
          </label>
          <label class="manager-field manager-import-row__field">
            <span>匹配路径</span>
            <input
              placeholder="按技能路径匹配，如 python/*,docs/*"
              :value="dialogState.preview.includePathPatternsText"
              type="text"
              @input="(event: Event) => handlePathPatternsInput((event.target as HTMLInputElement).value)"
            />
          </label>
        </div>

        <!-- 动作行与编辑弹窗「刷新预览」位置一致：匹配条件之后独立一行 -->
        <div v-if="dialogState.sourceKind === 'git'" class="manager-actions">
          <button
            class="secondary-button"
            :disabled="loading || dialogState.preview.previewLoading"
            type="button"
            @click="$emit('discoverGitSkills')"
          >
            {{ dialogState.preview.previewLoading ? "扫描中..." : "扫描来源" }}
          </button>
          <span v-if="summaryText" class="manager-inline-hint">{{ summaryText }}</span>
        </div>
      </template>
    </div>

    <template v-if="dialogState.sourceKind === 'batch-git'">
      <BatchGitImportResultList v-if="dialogState.batchResult" :result="dialogState.batchResult" />
    </template>

    <template v-else>
      <SkillDiscoveryPreview
        :discovery="dialogState.preview.discovery"
        :empty-text="dialogState.preview.previewLoading ? '正在读取来源...' : '先配置导入来源。'"
        :error="dialogState.preview.previewError"
        :loading="dialogState.preview.previewLoading"
      />
    </template>
  </DialogShell>
</template>
