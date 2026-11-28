<script setup lang="ts">
import { computed } from "vue";
import { formatTimestamp } from "@shared/lib/format";
import AppCard from "@shared/ui/AppCard.vue";
import { McpPathState, McpTransport } from "../types";
import type { McpConfigView, McpInspection } from "../types";

// MCP 卡片是展示面：装卸统一走同步弹窗（决策记录
// 2026-11-28-mcp-matrix-sync.md 第 4 条），这里只留状态 pill + 修改/删除。
type McpCardProps = {
  mcp: McpConfigView;
  inspection: McpInspection | null;
  loading?: boolean;
};

const props = withDefaults(defineProps<McpCardProps>(), {
  loading: false,
});

defineEmits<{
  editMcp: [mcp: McpConfigView];
  requestDeleteMcp: [serverNames: string[]];
}>();

function formatMcpSummary(): string {
  if (props.mcp.transport === McpTransport.TransportStdio) {
    const commandLine = [props.mcp.command, ...(props.mcp.args ?? [])].filter(Boolean).join(" ").trim();
    return commandLine || "stdio";
  }
  return props.mcp.url || (props.mcp.transport === McpTransport.TransportSSE ? "sse" : "http");
}

const createdAtLabel = computed(() => formatTimestamp(props.mcp.createdAt));
const summary = computed(formatMcpSummary);
// 状态 pill 不可点：present 高亮、missing 中性、error 置灰，完整原因在气泡里。
const pathStates = computed(() => props.inspection?.paths ?? []);
</script>

<template>
  <AppCard>
    <template #header>
      <span class="manager-skill-name">{{ mcp.name }}</span>
      <span v-if="mcp.createdAt" class="manager-skill-updated">
        新增于 {{ createdAtLabel }}
      </span>
    </template>
    <template #headerMeta>
      <span v-if="mcp.homepage" class="manager-header-path"><strong>主页</strong>{{ mcp.homepage }}</span>
    </template>

    <p class="manager-skill-description">{{ summary }}</p>
    <div class="manager-mcp-states">
      <span class="manager-mcp-states__label">MCP 路径</span>
      <span
        v-for="path in pathStates"
        :key="path.pathId"
        :class="`manager-mcp-state-pill${path.state === McpPathState.PathStatePresent ? ' is-present' : path.state === McpPathState.PathStateError ? ' is-error' : ''}`"
        :title="path.detail"
      >
        {{ path.pathId }}
      </span>
    </div>

    <template #actions>
      <button
        class="secondary-button"
        :disabled="loading"
        type="button"
        @click="$emit('editMcp', mcp)"
      >
        修改
      </button>
      <button
        class="danger-button"
        :disabled="loading"
        type="button"
        @click="$emit('requestDeleteMcp', [mcp.name])"
      >
        删除
      </button>
    </template>
  </AppCard>
</template>
