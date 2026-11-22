<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import ConfirmDialog from "@shared/ui/ConfirmDialog.vue";
import AppCard from "@shared/ui/AppCard.vue";
import AppFieldError from "@shared/ui/AppFieldError.vue";
import AppLoadError from "@shared/ui/AppLoadError.vue";
import AppResultBadge from "@shared/ui/AppResultBadge.vue";
import { extractErrorMessage } from "@shared/lib/errors";
import { applyProviderToApp, deleteProvider, upsertProvider } from "../../api";
import {
  appLabel,
  formFromProvider,
  formToInput,
  hasApiKey,
  initialSyncSelection,
  protocolLabel,
  providerSyncApps,
  providerSyncInput,
  providerSyncResultText,
  providerSyncTargets,
  emptyProviderForm,
  validateProviderForm,
  type ProviderFormState,
  type ProviderSyncTargets,
} from "../../model";
import { useProvidersAction, type RunProvidersActionOptions } from "../../composables/useProvidersAction";
import { ProviderWriteMode, type ProvidersState } from "@bindings/reins/internal/providers/models";
import ProviderEditDialog from "../dialogs/ProviderEditDialog.vue";
import ProviderSyncDialog from "../dialogs/ProviderSyncDialog.vue";

type ProvidersPanelProps = {
  state: ProvidersState | null;
  error: string | null;
  loadingState: boolean;
  runningAction: boolean;
  runAction: <T>(options: RunProvidersActionOptions<T>) => Promise<void>;
};

const props = defineProps<ProvidersPanelProps>();
defineEmits<{ retry: [] }>();

// 面板只消费根组件下传的状态与操作,不再自建或全局取数。
const state = computed(() => props.state);
const error = computed(() => props.error);
const loadingState = computed(() => props.loadingState);
const runningAction = computed(() => props.runningAction);
const runProvidersAction = props.runAction;

const editOpen = ref(false);
const editKeyPresent = ref(false);
// 新增流程分两步：step 1 服务商元数据，保存后进入 step 2 模型目录。
const editCreating = ref(false);
const editStep = ref<1 | 2>(1);
const form = reactive<ProviderFormState>(emptyProviderForm());
const deleteDialog = reactive({ open: false, providerId: "", providerLabel: "" });
const deleteError = ref<string | null>(null);
// 同步结果常驻在卡片内直到下次同步：异步任务结束后没有其他反馈位置。
const syncResults = reactive<
  Record<string, { message: string; tone: "body" | "danger" }>
>({});
// 同步弹窗：点「同步」先勾选模型，确认后才逐工具应用。
const syncDialog = reactive({ open: false, providerId: "" });
const syncError = ref<string | null>(null);

// 每个提供商的同步三态（目标/跳过清单/按钮提示）：已应用/漂移条目的工具
// 是候选，阻断只剩协议不兼容与未设 Key——模型取舍改由弹窗勾选表达；判定
// 与文案在 providerSyncTargets 一处，面板只做按提供商缓存。
const syncTargetsByProvider = computed(() => {
  const map = new Map<string, ProviderSyncTargets>();
  const current = state.value;
  if (current) {
    for (const provider of current.providers ?? []) {
      map.set(provider.id, providerSyncTargets(providerSyncApps(current, provider)));
    }
  }
  return map;
});

function syncTargetsFor(providerId: string): ProviderSyncTargets {
  // 查不到（state 未加载或提供商已不在列表）与「无候选」同口径。
  return syncTargetsByProvider.value.get(providerId) ?? providerSyncTargets([]);
}

const syncProvider = computed(
  () => (state.value?.providers ?? []).find((item) => item.id === syncDialog.providerId) ?? null
);

// 弹窗初始勾选：各参与工具已应用模型的并集，并集为空时给全目录。
const syncInitialSelection = computed(() => {
  const current = state.value;
  const provider = syncProvider.value;
  if (!current || !provider) {
    return [];
  }
  return initialSyncSelection(current, provider, syncTargetsFor(provider.id).targets);
});

function openSyncDialog(providerId: string) {
  syncDialog.providerId = providerId;
  syncError.value = null;
  syncDialog.open = true;
}

function openCreate() {
  Object.assign(form, emptyProviderForm());
  editCreating.value = true;
  editStep.value = 1;
  editOpen.value = true;
}

function openEdit(providerId: string) {
  const provider = (state.value?.providers ?? []).find((item) => item.id === providerId);
  if (!provider) {
    return;
  }
  Object.assign(form, formFromProvider(provider));
  editKeyPresent.value = hasApiKey(provider);
  editCreating.value = false;
  editStep.value = 2;
  editOpen.value = true;
}

function confirmEdit() {
  // 新增第一步只落库元数据，模型目录在第二步才填，不能在这一步拦住用户。
  const isCreateStepOne = editCreating.value && editStep.value === 1;
  form.errors = validateProviderForm(form, {
    allowEmptyModels: isCreateStepOne,
    existingProviderIds: (state.value?.providers ?? []).map((provider) => provider.id),
  });
  if (Object.keys(form.errors).length > 0) {
    return;
  }

  // 新增与编辑走同一条写入命令，意图显式下发：新增撞名会被后端拒绝。
  const input = formToInput(form, form.originalProviderId === null
      ? ProviderWriteMode.WriteModeCreate
      : ProviderWriteMode.WriteModeUpdate);
  runProvidersAction({
    // 元数据与明文 API Key 在同一份 providers.yaml 里，一次 upsert 原子落盘。
    action: () => upsertProvider(input),
    reload: true,
    onSuccess: (providerId) => {
      if (isCreateStepOne) {
        // 新增第一步：不关弹窗，原地进入模型目录设置；
        // 填上 originalProviderId 后，拉取即按已保存提供商走。
        form.originalProviderId = providerId;
        editKeyPresent.value = Boolean(form.apiKey.trim());
        editStep.value = 2;
        return;
      }
      editOpen.value = false;
    },
    onError: (message) => {
      // 后端拒绝时留在弹窗里，不再静默失败
      form.errors = { form: message };
    },
  });
}

function requestDeleteProvider(providerId: string) {
  const provider = (state.value?.providers ?? []).find((item) => item.id === providerId);
  deleteDialog.providerId = providerId;
  deleteDialog.providerLabel = provider?.label ?? providerId;
  deleteError.value = null;
  deleteDialog.open = true;
}

function closeDeleteDialog() {
  deleteDialog.open = false;
  deleteError.value = null;
}

// 同步是显式操作：按弹窗勾选逐个工具应用，后端按勾选清单整体重写条目
// （未勾选即移除）；成功后关弹窗，跳过与失败汇总成一句常驻写回卡片。
// 执行前先清掉该提供商的旧结果。
function runSync(modelIds: string[]) {
  const providerId = syncDialog.providerId;
  const current = state.value;
  const provider = syncProvider.value;
  if (!current || !provider) {
    return;
  }
  const { targets, skipLines } = syncTargetsFor(providerId);
  delete syncResults[providerId];
  runProvidersAction({
    action: async () => {
      const synced: string[] = [];
      const failures: string[] = [];
      for (const app of targets) {
        try {
          await applyProviderToApp(providerSyncInput(current, provider, app, modelIds));
          synced.push(appLabel(app));
        } catch (error) {
          failures.push(`${appLabel(app)}：${extractErrorMessage(error, "同步失败。")}`);
        }
      }
      return { synced, failures };
    },
    reload: true,
    onSuccess: ({ synced, failures }) => {
      syncDialog.open = false;
      syncResults[providerId] = {
        message: providerSyncResultText(synced, failures, skipLines),
        tone: failures.length > 0 ? "danger" : "body",
      };
    },
    onError: (message) => {
      // 失败留在弹窗里：勾选与说明原样保留，补齐前提后可原样重试。
      syncError.value = message;
    },
  });
}

function confirmDeleteProvider() {
  const providerId = deleteDialog.providerId;
  deleteError.value = null;
  runProvidersAction({
    action: () => deleteProvider(providerId),
    reload: true,
    onSuccess: () => {
      deleteDialog.open = false;
    },
    onError: (message) => {
      deleteError.value = message;
    },
  });
}
</script>

<template>
  <section class="providers-panel">
    <div class="providers-section-header">
      <div>
        <h3 class="providers-section-title">聚合提供商</h3>
        <p class="providers-section-hint">
          提供商元数据与 API Key（明文）保存在 {{ state?.configPath ?? "providers.yaml" }}。官方
          OpenAI / Anthropic 不在此纳管。
        </p>
      </div>
      <div class="providers-card__actions">
        <button class="primary-button" type="button" @click="openCreate">新增提供商</button>
      </div>
    </div>

    <AppLoadError
      v-if="error"
      :message="error"
      :retrying="loadingState"
      @retry="$emit('retry')"
    />

    <div v-if="loadingState" class="providers-loading">读取中…</div>

    <div v-else-if="state && (state.providers ?? []).length > 0" class="providers-card-grid">
      <AppCard v-for="provider in state.providers ?? []" :key="provider.id">
        <template #header>
          <h4 class="providers-card__title">{{ provider.label }}</h4>
        </template>
        <template #ext>
          <span class="providers-card__models">{{ (provider.models ?? []).length }} 个模型</span>
          <span class="providers-badge" :class="hasApiKey(provider) ? 'providers-badge--applied' : 'providers-badge--muted'">
            {{ hasApiKey(provider) ? "已设 Key" : "未设 Key" }}
          </span>
        </template>

        <dl class="providers-card__meta">
          <div>
            <dt>ID：</dt>
            <dd>{{ provider.id }}</dd>
          </div>
          <div>
            <dt>协议：</dt>
            <dd>{{ protocolLabel(provider.protocol) }}</dd>
          </div>
          <div class="providers-card__meta--wide">
            <dt>Base URL：</dt>
            <dd class="providers-card__url">{{ provider.baseUrl }}</dd>
          </div>
        </dl>

        <template #actions>
          <!-- 同步结果只占一个图标：文案在 hover 气泡里，操作行宽度不受文案长度影响 -->
          <AppResultBadge
            v-if="syncResults[provider.id]"
            :message="syncResults[provider.id].message"
            :tone="syncResults[provider.id].tone === 'danger' ? 'danger' : 'success'"
          />
          <button
            class="secondary-button"
            :disabled="runningAction || syncTargetsFor(provider.id).targets.length === 0"
            type="button"
            :title="syncTargetsFor(provider.id).buttonTitle"
            @click="openSyncDialog(provider.id)"
          >
            同步
          </button>
          <button class="secondary-button" :disabled="runningAction" type="button" @click="openEdit(provider.id)">
            编辑
          </button>
          <button class="danger-button" :disabled="runningAction" type="button" @click="requestDeleteProvider(provider.id)">
            删除提供商
          </button>
        </template>
      </AppCard>
    </div>

    <div v-else class="providers-empty">
      还没有聚合提供商。点击「新增提供商」添加 OpenRouter、自建网关等。
    </div>

    <ProviderEditDialog
      :open="editOpen"
      :loading="runningAction"
      :form="form"
      :key-present="editKeyPresent"
      :creating="editCreating"
      :step="editStep"
      @close="editOpen = false"
      @confirm="confirmEdit"
    />

    <!-- 打开时才挂载：open 与 providerId 同一次更新里置位，弹窗用 immediate 初始化 -->
    <ProviderSyncDialog
      v-if="syncProvider"
      :open="syncDialog.open"
      :loading="runningAction"
      :provider="syncProvider"
      :target-apps="syncTargetsFor(syncDialog.providerId).targets"
      :skip-lines="syncTargetsFor(syncDialog.providerId).skipLines"
      :initial-selected-ids="syncInitialSelection"
      :error="syncError"
      @close="syncDialog.open = false"
      @confirm="runSync"
    />

    <ConfirmDialog
      :open="deleteDialog.open"
      title-id="provider-delete-title"
      title="删除提供商"
      :description="`将删除提供商 ${deleteDialog.providerLabel} 的元数据与其中的明文 API Key。已写入工具的配置不会被自动清理；如 Claude Code 仍在引用该提供商，删除会被拒绝，请先在「Agent」页移除。`"
      confirm-label="删除"
      :loading="runningAction"
      @close="closeDeleteDialog"
      @confirm="confirmDeleteProvider"
    >
      <AppFieldError :message="deleteError" />
    </ConfirmDialog>
  </section>
</template>
