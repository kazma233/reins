<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import AppPageHeader from "@shared/ui/AppPageHeader.vue";
import { WORKSPACE_TAB_COPY } from "./model";
import type { WorkspaceTab } from "./types";
import { useWorkspaceState } from "./composables/useWorkspaceState";
import SkillsPanel from "./components/panels/SkillsPanel.vue";
import McpPanel from "./components/panels/McpPanel.vue";
import "./styles.css";

// tab 偏好跟着应用走:离开配置与分发再回来,停留的分页保持上次选择。
const workspaceTab = ref<WorkspaceTab>("skills");

// 面板区块头:标题与说明随页签切换
const workspaceTabCopy = computed(
  () => WORKSPACE_TAB_COPY.find((tab) => tab.id === workspaceTab.value)
);

// 数据状态由根组件持有一次;面板经 props 消费,写操作刷新同一份状态。
const workspace = useWorkspaceState();

onMounted(() => {
  void workspace.reloadWorkspaceState();
});
</script>

<template>
  <div class="manager-shell">
    <div class="manager-content">
      <header class="manager-content__header">
        <AppPageHeader
          meta="把 Skills 与 MCP 分发到各工具，技能路径与 MCP 路径互不约束"
          title="配置与分发"
        >
          <nav class="manager-nav">
            <button
              v-for="tab in WORKSPACE_TAB_COPY"
              :key="tab.id"
              :class="`manager-nav__button${workspaceTab === tab.id ? ' is-active' : ''}`"
              type="button"
              @click="workspaceTab = tab.id"
            >
              {{ tab.label }}
            </button>
          </nav>
        </AppPageHeader>
      </header>

      <!-- 面板区块头:左侧标题与说明,右侧是三个面板 Teleport 进来的动作按钮
           (挂点 div 必须常驻,面板挂载时才有落点);不进页头 -->
      <div class="manager-content__actions">
        <div class="manager-content__actions-main">
          <h3 class="manager-content__actions-title">
            {{ workspaceTabCopy?.title }}
          </h3>
          <p class="manager-content__actions-hint">
            {{ workspaceTabCopy?.hint }}
          </p>
        </div>
        <div id="workspace-panel-actions" class="manager-content__panel-actions"></div>
      </div>

      <SkillsPanel
        v-if="workspaceTab === 'skills'"
        :config-document="workspace.configDocument.value"
        :inspection="workspace.inspection.value"
        :loading-inspection="workspace.loadingInspection.value"
        :running-action="workspace.runningAction.value"
        :load-error="workspace.loadError.value"
        :workspace="workspace"
      />
      <McpPanel
        v-else-if="workspaceTab === 'mcp'"
        :config-document="workspace.configDocument.value"
        :inspection="workspace.inspection.value"
        :running-action="workspace.runningAction.value"
        :load-error="workspace.loadError.value"
        :workspace="workspace"
      />
    </div>
  </div>
</template>
