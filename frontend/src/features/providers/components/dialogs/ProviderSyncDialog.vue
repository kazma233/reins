<script setup lang="ts">
import { computed, ref, watch } from "vue";
import DialogShell from "@shared/ui/DialogShell.vue";
import { ProviderApp } from "@bindings/reins/internal/agents/models";
import type { ProviderView } from "@bindings/reins/internal/providers/models";
import { appLabel, CODEX_RESTART_HINT } from "../../model";

type ProviderSyncDialogProps = {
  open: boolean;
  loading: boolean;
  provider: ProviderView;
  // 本次参与同步的工具（前置条件齐备的候选）；勾选结果对每个工具整体生效。
  targetApps: ProviderApp[];
  // 不参与同步的工具与原因（协议不兼容、未设 Key），在弹窗里说明去向。
  skipLines: string[];
  // 初始勾选：各工具已应用模型的并集（并集为空时父层给全目录）。
  initialSelectedIds: string[];
  // 父层确认同步失败时写入的错误文案，展示在本弹窗内。
  error?: string | null;
};

const props = defineProps<ProviderSyncDialogProps>();

const emit = defineEmits<{
  close: [];
  confirm: [modelIds: string[]];
}>();

const selectedIds = ref<string[]>([]);

// 父组件在打开时才挂载本弹窗，open 与 provider 在同一次更新里置位；首次
// 挂载时 open 已是 true 而 watch 不会触发，必须 immediate 做首次初始化。
watch(
  () => props.open,
  (open) => {
    if (open) {
      selectedIds.value = [...props.initialSelectedIds];
    }
  },
  { immediate: true }
);

const targetsText = computed(() => props.targetApps.map(appLabel).join("、"));

// Codex 的模型目录由运行中的守护进程缓存，同步写入后同样要重启才生效。
const codexHint = computed(() =>
  props.targetApps.includes(ProviderApp.ProviderCodex) ? CODEX_RESTART_HINT : null
);

function toggleModel(modelId: string, checked: boolean) {
  if (checked) {
    if (!selectedIds.value.includes(modelId)) {
      selectedIds.value.push(modelId);
    }
  } else {
    selectedIds.value = selectedIds.value.filter((id) => id !== modelId);
  }
}

function confirm() {
  emit("confirm", selectedIds.value);
}
</script>

<template>
  <DialogShell
    :open="open"
    dialog-class-name="providers-sync-dialog"
    eyebrow="同步到工具"
    :title="`同步 ${provider.label} 的模型目录`"
    title-id="provider-sync-dialog-title"
    :close-disabled="loading"
    @close="$emit('close')"
  >
    <template #actions>
      <!-- 确认同步失败的回执跟在按钮旁边，操作行宽度不受文案长度影响 -->
      <p v-if="error" class="providers-apply-hint">{{ error }}</p>
      <button
        class="primary-button"
        :disabled="loading || selectedIds.length === 0"
        type="button"
        @click="confirm"
      >
        确认同步
      </button>
    </template>

    <p class="providers-section-hint">
      参与同步：{{ targetsText }}。勾选的模型会写入并保留在各工具的本提供商条目里，未勾选的会被移除；各工具的默认模型与思考等级保持当前值。
    </p>
    <p v-if="skipLines.length > 0" class="providers-section-hint providers-sync-skips">
      未同步：{{ skipLines.join("；") }}。
    </p>
    <p v-if="codexHint" class="providers-section-hint">{{ codexHint }}</p>

    <ul v-if="selectedIds.length === 0" class="providers-blockers">
      <li>至少选择一个模型。</li>
    </ul>

    <div class="providers-model-list">
      <label
        v-for="model in provider.models ?? []"
        :key="model.id"
        class="providers-fetched-item"
      >
        <input
          type="checkbox"
          :checked="selectedIds.includes(model.id)"
          :disabled="loading"
          @change="toggleModel(model.id, ($event.target as HTMLInputElement).checked)"
        />
        <span>
          {{ model.id }}<template v-if="model.label && model.label !== model.id"> · {{ model.label }}</template>
        </span>
      </label>
    </div>
  </DialogShell>
</template>
