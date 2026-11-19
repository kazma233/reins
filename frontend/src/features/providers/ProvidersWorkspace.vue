<script setup lang="ts">
import { onMounted, ref } from "vue";
import AppPageHeader from "@shared/ui/AppPageHeader.vue";
import { PROVIDERS_TAB_COPY, type ProvidersTab } from "./model";
import { useProvidersState } from "./composables/useProvidersState";
import { useProvidersAction } from "./composables/useProvidersAction";
import ProvidersPanel from "./components/panels/ProvidersPanel.vue";
import AppsPanel from "./components/panels/AppsPanel.vue";
import "./styles.css";

// tab 偏好跟着应用走:离开模型配置再回来,停留的分页保持上次选择。
const providersTab = ref<ProvidersTab>("apps");

const providers = useProvidersState();
const { runProvidersAction } = useProvidersAction(providers);

onMounted(() => {
  void providers.reloadProvidersState();
});
</script>

<template>
  <div class="providers-shell">
    <div class="providers-content">
      <header class="providers-content__header">
        <AppPageHeader
          meta="集中管理聚合提供商的模型配置并一键应用到各工具"
          title="模型配置"
        >
          <nav class="providers-nav">
            <button
              v-for="tab in PROVIDERS_TAB_COPY"
              :key="tab.id"
              :class="`providers-nav__button${providersTab === tab.id ? ' is-active' : ''}`"
              type="button"
              @click="providersTab = tab.id"
            >
              {{ tab.label }}
            </button>
          </nav>
        </AppPageHeader>
      </header>

      <!-- 页头在滚动流之外,滚动条只属于下方内容区,不再从窗口顶划到头 -->
      <div class="providers-content__body">
        <ProvidersPanel
          v-if="providersTab === 'providers'"
          :state="providers.state.value"
          :error="providers.error.value"
          :loading-state="providers.loadingState.value"
          :running-action="providers.runningAction.value"
          :run-action="runProvidersAction"
          @retry="providers.retryProvidersState"
        />
        <AppsPanel
          v-else
          :state="providers.state.value"
          :error="providers.error.value"
          :app-errors="providers.appErrors.value"
          :loading-state="providers.loadingState.value"
          :running-action="providers.runningAction.value"
          :run-action="runProvidersAction"
          @retry="providers.retryProvidersState"
          @reload-app="providers.reloadProviderAppState"
        />
      </div>
    </div>
  </div>
</template>
