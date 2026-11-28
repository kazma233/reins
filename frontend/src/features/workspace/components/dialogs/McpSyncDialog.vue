<script setup lang="ts">
import { computed, ref, watch } from "vue";
import DialogShell from "@shared/ui/DialogShell.vue";
import { formatMcpTransport } from "../../model";
import {
  buildMcpCellStates,
  cellDisabledReason,
  collectCheckedCells,
  countCheckedCells,
  defaultCheckedCells,
  isCellChecked,
  isCellSelectable,
  setCellsChecked,
  toggleCell,
} from "../../mcpSync";
import type {
  McpConfigView,
  McpPathView,
  McpSyncCell,
  WorkspaceConfigDocument,
  WorkspaceInspection,
} from "../../types";

type McpSyncDialogProps = {
  open: boolean;
  configDocument: WorkspaceConfigDocument | null;
  inspection: WorkspaceInspection | null;
  // 父级在预演/执行期间的 loading，锁住整个矩阵。
  loading: boolean;
};

const props = defineProps<McpSyncDialogProps>();

const emit = defineEmits<{
  close: [];
  confirm: [cells: McpSyncCell[]];
}>();

const servers = computed<McpConfigView[]>(() => props.configDocument?.config?.mcps ?? []);
const paths = computed<McpPathView[]>(() => props.configDocument?.config?.mcpPaths ?? []);
const cellStates = computed(() => buildMcpCellStates(props.inspection));

// 勾选集合就是期望态：打开弹窗时按「已装格子全勾」初始化（默认动作 = 纯
// 刷新），之后随用户编辑；同步后的状态刷新经 props 就地反映，勾选不重置。
const checked = ref(new Map<string, Set<string>>());
const confirming = ref(false);

// 结果槽位：执行结果由父级经 defineExpose 回写；弹窗不关，列表就地更新。
const resultMessage = ref<{ text: string; failed: boolean } | null>(null);
function reportResult(result: { text: string; failed: boolean } | null) {
  resultMessage.value = result;
}
defineExpose({ reportResult });

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    checked.value = defaultCheckedCells(servers.value, paths.value, cellStates.value);
    resultMessage.value = null;
  },
);

const busy = computed(() => confirming.value || props.loading);

function pathItemOf(serverName: string, pathId: string) {
  return cellStates.value.get(serverName)?.get(pathId) ?? null;
}

const pathByName = computed(() => new Map(paths.value.map((path) => [path.name, path])));

function isSelectable(server: McpConfigView, path: McpPathView): boolean {
  return isCellSelectable(server.transport, path.sseUnsupported, pathItemOf(server.name, path.name));
}

function selectablePathIdsFor(server: McpConfigView): string[] {
  return paths.value.filter((path) => isSelectable(server, path)).map((path) => path.name);
}

function selectableServerNamesFor(pathId: string): string[] {
  const path = pathByName.value.get(pathId);
  if (!path) return [];
  return servers.value.filter((server) => isSelectable(server, path)).map((server) => server.name);
}

function rowAllChecked(server: McpConfigView): boolean {
  const ids = selectablePathIdsFor(server);
  return ids.length > 0 && ids.every((id) => isCellChecked(checked.value, server.name, id));
}

function columnAllChecked(pathId: string): boolean {
  const names = selectableServerNamesFor(pathId);
  return names.length > 0 && names.every((name) => isCellChecked(checked.value, name, pathId));
}

const allSelectableChecked = computed(
  () =>
    servers.value.length > 0 &&
    paths.value.length > 0 &&
    servers.value.every((server) => rowAllChecked(server)),
);

function toggleRow(server: McpConfigView) {
  checked.value = setCellsChecked(
    checked.value,
    [server.name],
    selectablePathIdsFor(server),
    !rowAllChecked(server),
  );
}

function toggleColumn(pathId: string) {
  checked.value = setCellsChecked(
    checked.value,
    selectableServerNamesFor(pathId),
    [pathId],
    !columnAllChecked(pathId),
  );
}

// 全选/清空只作用于可选格子，禁用格（error、pi+SSE）永远不在勾选集合里。
function toggleAll() {
  const selected = !allSelectableChecked.value;
  let next = checked.value;
  for (const server of servers.value) {
    next = setCellsChecked(next, [server.name], selectablePathIdsFor(server), selected);
  }
  checked.value = next;
}

const checkedCount = computed(() => countCheckedCells(checked.value));

const canConfirm = computed(
  () => !busy.value && servers.value.length > 0 && paths.value.length > 0,
);

function handleConfirm() {
  confirming.value = true;
  resultMessage.value = null;
  const cells = collectCheckedCells(checked.value).filter((cell) => {
    const server = servers.value.find((item) => item.name === cell.serverName);
    const path = pathByName.value.get(cell.pathId);
    return server && path ? isSelectable(server, path) : false;
  });
  Promise.resolve(emit("confirm", cells)).finally(() => {
    confirming.value = false;
  });
}
</script>

<template>
  <DialogShell
    :open="open"
    dialog-class-name="manager-import-dialog manager-sync-dialog"
    eyebrow="MCP 同步"
    title="同步 MCP 服务到 MCP 路径"
    title-id="mcp-sync-dialog-title"
    :close-disabled="busy"
    @close="emit('close')"
  >
    <template #actions>
      <span
        v-if="resultMessage"
        :class="`manager-sync-result${resultMessage.failed ? ' manager-sync-result--failed' : ''}`"
        role="status"
      >
        {{ resultMessage.text }}
      </span>
      <button class="primary-button" :disabled="!canConfirm" type="button" @click="handleConfirm">
        {{ confirming ? "同步中..." : "开始同步" }}
      </button>
    </template>

    <template v-if="servers.length === 0">
      <div class="empty-state">还没有登记 MCP 服务。先「新增 MCP 服务」再回来同步。</div>
    </template>
    <template v-else-if="paths.length === 0">
      <div class="empty-state">还没有登记 MCP 路径。先「新增 MCP 路径」再回来同步。</div>
    </template>
    <template v-else>
      <p class="manager-sync-dialog__hint">
        勾选就是期望状态：每条路径收敛到该列勾选——勾选写入/刷新，取消勾选已装条目会在同步时移除。
        默认已装全勾（纯刷新）；读取失败的路径整列禁用。
      </p>
      <div class="manager-mcp-sync__scroll">
        <table class="manager-mcp-sync-table">
          <thead>
            <tr>
              <th class="manager-mcp-sync-table__corner" scope="col">
                <label class="manager-mcp-sync-table__check">
                  <input
                    type="checkbox"
                    :checked="allSelectableChecked"
                    :disabled="busy"
                    @change="toggleAll"
                  />
                  <span>服务 \ 路径</span>
                </label>
              </th>
              <th v-for="path in paths" :key="path.name" class="manager-mcp-sync-table__col" scope="col">
                <label
                  class="manager-mcp-sync-table__check"
                  :title="`${path.configPath}${path.configPrefix ? `\n前缀：${path.configPrefix}` : ''}\n${path.mcpFormatDescription}`"
                >
                  <input
                    type="checkbox"
                    :checked="columnAllChecked(path.name)"
                    :disabled="busy"
                    @change="toggleColumn(path.name)"
                  />
                  <span>{{ path.name }}</span>
                </label>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="server in servers" :key="server.name">
              <th class="manager-mcp-sync-table__row" scope="row">
                <label class="manager-mcp-sync-table__check">
                  <input
                    type="checkbox"
                    :checked="rowAllChecked(server)"
                    :disabled="busy"
                    @change="toggleRow(server)"
                  />
                  <span :title="formatMcpTransport(server.transport)">{{ server.name }}</span>
                </label>
              </th>
              <td
                v-for="path in paths"
                :key="path.name"
                :class="`manager-mcp-sync-table__cell is-${pathItemOf(server.name, path.name)?.state ?? 'missing'}`"
              >
                <label
                  v-if="isSelectable(server, path)"
                  class="manager-mcp-sync-table__check manager-mcp-sync-table__check--center"
                  :title="pathItemOf(server.name, path.name)?.detail"
                >
                  <input
                    type="checkbox"
                    :checked="isCellChecked(checked, server.name, path.name)"
                    :disabled="busy"
                    @change="checked = toggleCell(checked, server.name, path.name)"
                  />
                </label>
                <span
                  v-else
                  class="manager-mcp-sync-table__blocked"
                  :title="cellDisabledReason(server.transport, path.sseUnsupported, pathItemOf(server.name, path.name))"
                >
                  —
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="manager-mcp-sync__count">已勾选 {{ checkedCount }} 格</p>
    </template>
  </DialogShell>
</template>
