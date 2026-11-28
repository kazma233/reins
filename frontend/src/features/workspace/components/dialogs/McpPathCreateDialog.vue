<script setup lang="ts">
import { computed } from "vue";
import { type McpPathFormState } from "../../model";
import { joinClasses } from "@shared/lib/join-classes";
import type { McpFormatExample, McpFormatInfo, McpPathPreset } from "../../types";
import AppFieldError from "@shared/ui/AppFieldError.vue";
import AppLoadError from "@shared/ui/AppLoadError.vue";
import DialogShell from "@shared/ui/DialogShell.vue";

type McpPathCreateDialogProps = {
  form: McpPathFormState;
  open: boolean;
  loading: boolean;
  error: string | null;
  // 创建模式：后端下发的内置工具预设（路径是 env 解析后的真实值）。
  presets: McpPathPreset[];
  presetsLoading: boolean;
  presetsError: string | null;
  // 创建模式：全部配置格式的说明与写入示例（switch 展开内容）。
  formatInfos: McpFormatInfo[];
  formatInfosLoading: boolean;
  formatInfosError: string | null;
  // 编辑模式：存量条目按实际前缀生成的说明与示例。
  formatDescription: string | null;
  formatExamples: McpFormatExample[];
};

const props = defineProps<McpPathCreateDialogProps>();

defineEmits<{
  close: [];
  confirm: [];
  clearFieldError: [field: string];
  applyPreset: [presetName: string];
  selectConfigType: [configType: string];
  retryLoadPresets: [];
  retryLoadFormatInfos: [];
  pickConfigFile: [];
}>();

const dialogTitle = computed(() => (props.form.originalName ? "修改 MCP 路径" : "新增 MCP 路径"));
const confirmLabel = computed(() => (props.form.originalName ? "保存" : "添加"));
const isCreate = computed(() => props.form.originalName === null);

// 当前选中格式的展示内容：创建模式从下发的格式信息里取（示例用该格式的
// 代表前缀生成），编辑模式用存量条目按实际前缀生成的 props。
const activeFormat = computed(() =>
  isCreate.value
    ? (props.formatInfos.find((info) => info.configType === props.form.configType) ?? null)
    : null,
);
const activeDescription = computed(() =>
  isCreate.value ? (activeFormat.value?.description ?? null) : props.formatDescription,
);
const activeExamples = computed<McpFormatExample[]>(() =>
  isCreate.value ? (activeFormat.value?.formatExamples ?? []) : (props.formatExamples ?? []),
);
</script>

<template>
  <DialogShell
    :open="open"
    dialog-class-name="manager-import-dialog"
    eyebrow="MCP 路径"
    :title="dialogTitle"
    title-id="mcp-path-create-dialog-title"
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
          点击预设回填名称、配置文件路径与格式，全部都可修改；也可以完全自定义任意配置文件。
        </small>
      </div>

      <div class="manager-stack manager-target-form-fields">
        <label class="manager-field">
          <span>
            名称
            <span class="manager-required-mark"> *</span>
            <AppFieldError
              class="manager-field__error"
              id="mcp-path-name-error"
              :message="form.errors.name ?? null"
            />
          </span>
          <small class="manager-field__hint">这条 MCP 路径的显示名。</small>
          <input
            v-model="form.name"
            :aria-describedby="form.errors.name ? 'mcp-path-name-error' : undefined"
            :aria-invalid="Boolean(form.errors.name)"
            type="text"
            @input="$emit('clearFieldError', 'name')"
          />
        </label>

        <div class="manager-picker-row">
          <label class="manager-field manager-picker-row__field">
            <span>
              MCP 配置文件
              <span class="manager-required-mark"> *</span>
              <AppFieldError
                class="manager-field__error"
                id="mcp-path-config-error"
                :message="form.errors.configPath ?? null"
              />
            </span>
            <small class="manager-field__hint">MCP 条目写进这个配置文件。</small>
            <input
              v-model="form.configPath"
              :aria-describedby="form.errors.configPath ? 'mcp-path-config-error' : undefined"
              :aria-invalid="Boolean(form.errors.configPath)"
              type="text"
              @input="$emit('clearFieldError', 'configPath')"
            />
          </label>
          <div class="manager-actions manager-picker-row__actions">
            <button
              class="secondary-button"
              :disabled="loading"
              type="button"
              @click="$emit('pickConfigFile')"
            >
              选择配置
            </button>
          </div>
        </div>

        <div v-if="isCreate" class="manager-field">
          <span class="manager-field__label">
            配置格式
            <span class="manager-required-mark"> *</span>
            <AppFieldError
              class="manager-field__error"
              id="mcp-path-type-error"
              :message="form.errors.configType ?? null"
            />
          </span>
          <div v-if="formatInfosLoading" class="loading-pill">正在读取配置格式...</div>
          <AppLoadError
            v-else-if="formatInfosError"
            :message="formatInfosError"
            :retrying="formatInfosLoading"
            @retry="$emit('retryLoadFormatInfos')"
          />
          <div v-else class="manager-card-actions manager-card-actions--start">
            <button
              v-for="info in formatInfos"
              :key="info.configType"
              :class="joinClasses('manager-target-button', form.configType === info.configType && 'is-active')"
              :disabled="loading"
              :title="info.description"
              type="button"
              @click="$emit('selectConfigType', info.configType)"
            >
              {{ info.configType }}
            </button>
          </div>
          <small class="manager-field__hint">
            配置文件的条目形态由格式决定；创建后不可更换。示例按该格式的约定前缀生成。
          </small>
          <template v-if="activeDescription">
            <small class="manager-field__hint">{{ activeDescription }}</small>
            <div class="manager-format-examples">
              <div
                v-for="example in activeExamples"
                :key="example.label"
                class="manager-format-examples__item"
              >
                <small class="manager-field__hint">{{ example.label }}</small>
                <pre class="manager-pre">{{ example.body }}</pre>
              </div>
            </div>
          </template>
        </div>

        <label class="manager-field">
          <span>configPrefix</span>
          <small class="manager-field__hint">
            写入配置文件的节点路径。部分格式（按文件结构定位条目）没有前缀概念，留空即可；预设回填的值是该工具的固定约定。
          </small>
          <input
            v-model="form.configPrefix"
            type="text"
            @input="$emit('clearFieldError', 'configPrefix')"
          />
        </label>

        <div v-if="formatDescription" class="manager-stack">
          <span class="manager-field__label">MCP 配置格式</span>
          <small class="manager-field__hint">{{ formatDescription }}</small>
        </div>
      </div>
    </div>
  </DialogShell>
</template>
