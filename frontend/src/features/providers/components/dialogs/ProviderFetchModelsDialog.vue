<script setup lang="ts">
import { computed, ref, watch } from "vue";
import DialogShell from "@shared/ui/DialogShell.vue";
import { extractErrorMessage } from "@shared/lib/errors";
import { fetchProviderModelsDirect } from "../../api";
import { hasModelId } from "../../model";
import type {
  FetchedModel,
  FetchModelsAttempt,
  ProviderProtocol,
} from "@bindings/reins/internal/providers/models";

type ProviderFetchModelsDialogProps = {
  open: boolean;
  protocol: ProviderProtocol;
  baseUrl: string;
  // 拉取用表单当前值（含未保存的改动）：编辑态若读 providers.yaml 里已保存的
  // 那一份，用户刚改的 Base URL / 协议 / Key 就不会生效。
  apiKey: string;
  // 当前模型目录里已有的模型 ID：命中者以勾选且禁用的状态展示，不可重复加入。
  existingIds: string[];
};

const props = defineProps<ProviderFetchModelsDialogProps>();

const emit = defineEmits<{
  close: [];
  confirm: [models: FetchedModel[]];
}>();

const fetching = ref(false);
const errorText = ref("");
const models = ref<FetchedModel[]>([]);
const fetchedUrl = ref("");
const attempts = ref<FetchModelsAttempt[]>([]);
const selected = ref<Record<string, boolean>>({});

watch(
  () => props.open,
  (open) => {
    if (open) {
      models.value = [];
      fetchedUrl.value = "";
      attempts.value = [];
      selected.value = {};
      errorText.value = "";
      void runFetch();
    }
  }
);

async function runFetch() {
  fetching.value = true;
  errorText.value = "";
  attempts.value = [];
  try {
    // 始终用表单当前的协议 / Base URL / Key 直连拉取，保存过的提供商也如此：
    // 用户改了表单还没保存时，拉取要按改后的值走。
    // 后端可能经历多级候选路径回退，url 是实际命中的那一个。
    const result = await fetchProviderModelsDirect(
      props.protocol,
      props.baseUrl.trim(),
      props.apiKey.trim()
    );
    attempts.value = result.attempts ?? [];
    // 请求失败也走返回值：失败弹窗要按 URI 逐条列举请求与响应，
    // Wails 的 error 只能带一条字符串。
    if (result.error) {
      errorText.value = result.error;
      models.value = [];
      fetchedUrl.value = "";
      return;
    }
    models.value = result.models ?? [];
    fetchedUrl.value = result.url;
    selected.value = {};
  } catch (error) {
    errorText.value = extractErrorMessage(error, "拉取模型列表失败。");
  } finally {
    fetching.value = false;
  }
}

// 一次候选请求的响应描述：传输层失败 status=0 没有响应；其余带状态码，
// 响应摘要（响应体开头 / 上游错误）已在后端脱敏。
function describeAttempt(attempt: FetchModelsAttempt): string {
  if (attempt.status === 0) {
    return attempt.detail || "未收到响应";
  }
  const status = `HTTP ${attempt.status}${attempt.statusText ? ` ${attempt.statusText}` : ""}`;
  return attempt.detail ? `${status} · ${attempt.detail}` : status;
}

const selectedModels = computed(() => models.value.filter((model) => selected.value[model.id]));

function isExistingId(modelId: string): boolean {
  return hasModelId(props.existingIds, modelId);
}

function confirm() {
  // 按声明 emit FetchedModel[]（含 name），显示名的取舍交给父层。
  emit("confirm", selectedModels.value);
}
</script>

<template>
  <DialogShell
    :open="open"
    dialog-class-name="providers-fetch-dialog"
    eyebrow="从提供商拉取"
    title="选择要加入的模型"
    title-id="provider-fetch-models-dialog-title"
    @close="$emit('close')"
  >
    <template #actions>
      <button
        class="primary-button"
        :disabled="fetching || selectedModels.length === 0"
        type="button"
        @click="confirm"
      >
        加入已选（{{ selectedModels.length }}）
      </button>
    </template>

    <div v-if="fetching" class="providers-loading">拉取中…</div>

    <template v-else-if="errorText">
      <p class="providers-empty">{{ errorText }}</p>
      <div v-if="attempts.length > 0" class="providers-attempts">
        <p class="providers-section-hint">实际请求（{{ attempts.length }} 次）</p>
        <ul class="providers-fetched-list providers-attempt-list">
          <li v-for="(attempt, index) in attempts" :key="index" class="providers-attempt">
            <span class="providers-attempt__url">GET {{ attempt.url }}</span>
            <span class="providers-attempt__resp">{{ describeAttempt(attempt) }}</span>
          </li>
        </ul>
      </div>
      <button class="secondary-button" type="button" @click="runFetch">重试</button>
    </template>

    <template v-else-if="models.length === 0">
      <p class="providers-empty">提供商返回了空的模型列表。</p>
      <button class="secondary-button" type="button" @click="runFetch">重试</button>
    </template>

    <div v-else class="providers-fetched-list">
      <p class="providers-section-hint">拉取地址：{{ fetchedUrl }}</p>
      <template v-for="model in models" :key="model.id">
        <label v-if="!isExistingId(model.id)" class="providers-fetched-item">
          <input v-model="selected[model.id]" type="checkbox" />
          <span>{{ model.id }}<template v-if="model.name"> · {{ model.name }}</template></span>
        </label>
        <label v-else class="providers-fetched-item providers-fetched-item--existing">
          <input type="checkbox" :checked="true" disabled />
          <span>{{ model.id }}<template v-if="model.name"> · {{ model.name }}</template></span>
        </label>
      </template>
    </div>
  </DialogShell>
</template>
