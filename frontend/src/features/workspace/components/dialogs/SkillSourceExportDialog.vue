<script setup lang="ts">
import { computed, ref } from "vue";
import DialogShell from "@shared/ui/DialogShell.vue";
import { buildSkillSourcesBatchJson } from "../../model";
import type { SkillSourceConfigView } from "../../types";

type SkillSourceExportDialogProps = {
  open: boolean;
  sources: SkillSourceConfigView[];
};

const props = defineProps<SkillSourceExportDialogProps>();

defineEmits<{
  close: [];
}>();

const jsonText = computed(() => buildSkillSourcesBatchJson(props.sources));

// 复制反馈走按钮自身文案（复制 → 已复制/复制失败），不另设状态行；
// 弹窗 actions 右对齐，按钮宽度变化只向空白侧伸展，不推挤任何控件。
const copied = ref(false);
const copyFailed = ref(false);
let copiedTimer: number | undefined;

function resetCopyFeedbackLater() {
  window.clearTimeout(copiedTimer);
  copiedTimer = window.setTimeout(() => {
    copied.value = false;
    copyFailed.value = false;
  }, 1500);
}

async function copyJson() {
  if (!jsonText.value) return;
  try {
    await navigator.clipboard.writeText(jsonText.value);
    copied.value = true;
  } catch {
    copyFailed.value = true;
  }
  resetCopyFeedbackLater();
}

const copyLabel = computed(() => {
  if (copyFailed.value) return "复制失败";
  if (copied.value) return "已复制";
  return "复制 JSON";
});
</script>

<template>
  <DialogShell
    :open="open"
    dialog-class-name="manager-import-dialog"
    eyebrow="Source"
    title="导出批量导入 JSON"
    title-id="skill-source-export-dialog-title"
    @close="$emit('close')"
  >
    <template #actions>
      <button
        class="primary-button"
        :disabled="!jsonText"
        type="button"
        @click="copyJson"
      >
        {{ copyLabel }}
      </button>
    </template>

    <div class="manager-import-shell manager-import-shell--compact">
      <p class="manager-skill-description">
        导出全部远端来源；复制保存为 JSON 文件，或在「新增来源 → 批量 Git」中直接粘贴。
      </p>
      <pre class="manager-pre manager-export-json-pre">{{ jsonText }}</pre>
    </div>
  </DialogShell>
</template>
