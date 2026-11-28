<script setup lang="ts">
import { computed } from "vue";
import { type SkillPathFormState } from "../../model";
import type { SkillPathPreset } from "../../types";
import AppFieldError from "@shared/ui/AppFieldError.vue";
import AppLoadError from "@shared/ui/AppLoadError.vue";
import DialogShell from "@shared/ui/DialogShell.vue";

type SkillPathCreateDialogProps = {
  form: SkillPathFormState;
  open: boolean;
  loading: boolean;
  error: string | null;
  // 创建模式：后端下发的内置工具预设（目录是 env 解析后的真实值）。
  presets: SkillPathPreset[];
  presetsLoading: boolean;
  presetsError: string | null;
};

const props = defineProps<SkillPathCreateDialogProps>();

defineEmits<{
  close: [];
  confirm: [];
  clearFieldError: [field: string];
  applyPreset: [presetName: string];
  retryLoadPresets: [];
  pickSkillDirectory: [];
}>();

const dialogTitle = computed(() => (props.form.originalName ? "修改技能路径" : "新增技能路径"));
const confirmLabel = computed(() => (props.form.originalName ? "保存" : "添加"));
const isCreate = computed(() => props.form.originalName === null);
</script>

<template>
  <DialogShell
    :open="open"
    dialog-class-name="manager-import-dialog"
    eyebrow="技能路径"
    :title="dialogTitle"
    title-id="skill-path-create-dialog-title"
    :close-disabled="loading"
    @close="$emit('close')"
  >
    <template #actions>
      <button
        class="primary-button"
        :disabled="loading"
        type="button"
        @click="$emit('confirm')"
      >
        {{ confirmLabel }}
      </button>
    </template>

    <div class="manager-target-form-shell">
      <AppFieldError :message="error" />

      <div v-if="isCreate" class="manager-stack">
        <span class="manager-field__label">预设</span>
        <div v-if="presetsLoading" class="loading-pill">正在读取预设...</div>
        <AppLoadError
          v-else-if="presetsError"
          :message="presetsError"
          :retrying="presetsLoading"
          @retry="$emit('retryLoadPresets')"
        />
        <div v-else class="manager-card-actions manager-card-actions--start">
          <button
            v-for="preset in presets"
            :key="preset.name"
            class="secondary-button"
            :disabled="loading"
            type="button"
            @click="$emit('applyPreset', preset.name)"
          >
            {{ preset.name }}
          </button>
        </div>
        <small class="manager-field__hint">
          点击预设回填名称与目录，两者都可修改；也可以完全自定义任意目录——skill 会以目录链接装进这里。
        </small>
      </div>

      <div class="manager-stack manager-target-form-fields">
        <label class="manager-field">
          <span>
            名称
            <span class="manager-required-mark"> *</span>
            <AppFieldError
              class="manager-field__error"
              id="skill-path-name-error"
              :message="form.errors.name ?? null"
            />
          </span>
          <small class="manager-field__hint">这条技能路径的显示名，例如工具名或用途。</small>
          <input
            v-model="form.name"
            :aria-describedby="form.errors.name ? 'skill-path-name-error' : undefined"
            :aria-invalid="Boolean(form.errors.name)"
            type="text"
            @input="$emit('clearFieldError', 'name')"
          />
        </label>

        <div class="manager-picker-row">
          <label class="manager-field manager-picker-row__field">
            <span>
              技能目录
              <span class="manager-required-mark"> *</span>
              <AppFieldError
                class="manager-field__error"
                id="skill-path-dir-error"
                :message="form.errors.dir ?? null"
              />
            </span>
            <small class="manager-field__hint">skill 会以目录链接装进这个目录。</small>
            <input
              v-model="form.dir"
              :aria-describedby="form.errors.dir ? 'skill-path-dir-error' : undefined"
              :aria-invalid="Boolean(form.errors.dir)"
              type="text"
              @input="$emit('clearFieldError', 'dir')"
            />
          </label>
          <div class="manager-actions manager-picker-row__actions">
            <button
              class="secondary-button"
              :disabled="loading"
              type="button"
              @click="$emit('pickSkillDirectory')"
            >
              选择目录
            </button>
          </div>
        </div>
      </div>
    </div>
  </DialogShell>
</template>
