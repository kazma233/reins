import { McpPathState, McpTransport } from "./types";
import type {
  McpConfigView,
  McpPathStateInspection,
  McpPathView,
  McpSyncCell,
  WorkspaceInspection,
} from "./types";

// MCP 矩阵同步的格子逻辑（纯模块）：格子状态来自 inspection，勾选集合就是
// 期望态——同步 = 每条路径收敛到该列勾选集合（决策记录
// 2026-11-28-mcp-matrix-sync.md 第 2 条）。

// server 名 → pathId → 该格的巡检状态。
export type McpCellStateIndex = Map<string, Map<string, McpPathStateInspection>>;

// server 名 → 勾选的 pathId 集合（期望态；同步入参由它展开）。
export type McpCheckedCells = Map<string, Set<string>>;

export function buildMcpCellStates(inspection: WorkspaceInspection | null): McpCellStateIndex {
  const index: McpCellStateIndex = new Map();
  for (const mcp of inspection?.mcps ?? []) {
    index.set(mcp.name, new Map((mcp.paths ?? []).map((path) => [path.pathId, path])));
  }
  return index;
}

// SSE 防呆：该配置文件对应的工具不接受 legacy SSE transport（知识归后端
// 清单，路径视图下发 sseUnsupported），写进去会被工具拒连，格子禁用。
function isSseUnsupported(transport: McpTransport, sseUnsupported: boolean): boolean {
  return transport === McpTransport.TransportSSE && sseUnsupported;
}

// isCellSelectable：格子是否可勾。读取失败的路径没有可判定的状态，整格
// 禁用而不是点击后报错。
export function isCellSelectable(
  transport: McpTransport,
  sseUnsupported: boolean,
  pathItem: McpPathStateInspection | null | undefined,
): boolean {
  if (pathItem?.state === McpPathState.PathStateError) return false;
  return !isSseUnsupported(transport, sseUnsupported);
}

export function cellDisabledReason(
  transport: McpTransport,
  sseUnsupported: boolean,
  pathItem: McpPathStateInspection | null | undefined,
): string {
  if (pathItem?.state === McpPathState.PathStateError) {
    return pathItem.detail || "读取目标配置失败。";
  }
  return "该工具不接受 SSE transport 的 MCP。";
}

// defaultCheckedCells：打开弹窗的默认勾选 = 已装（present）且可选的格子。
// 默认动作因此是「纯刷新」，零传播零意外；移除通过取消勾选表达。
export function defaultCheckedCells(
  servers: McpConfigView[],
  paths: McpPathView[],
  cellStates: McpCellStateIndex,
): McpCheckedCells {
  const checked: McpCheckedCells = new Map();
  for (const server of servers) {
    const states = cellStates.get(server.name);
    for (const path of paths) {
      const state = states?.get(path.name);
      if (state?.state !== McpPathState.PathStatePresent) continue;
      if (!isCellSelectable(server.transport, path.sseUnsupported, state)) continue;
      checked.set(server.name, new Set([...(checked.get(server.name) ?? []), path.name]));
    }
  }
  return checked;
}

export function isCellChecked(checked: McpCheckedCells, serverName: string, pathId: string): boolean {
  return checked.get(serverName)?.has(pathId) ?? false;
}

export function toggleCell(checked: McpCheckedCells, serverName: string, pathId: string): McpCheckedCells {
  const next = new Map([...checked].map(([name, paths]) => [name, new Set(paths)]));
  const paths = new Set(next.get(serverName) ?? []);
  if (paths.has(pathId)) paths.delete(pathId);
  else paths.add(pathId);
  next.set(serverName, paths);
  return next;
}

// setCellsChecked：行/列头的批量勾选（只作用于传入的格子组合）。
export function setCellsChecked(
  checked: McpCheckedCells,
  serverNames: string[],
  pathIds: string[],
  selected: boolean,
): McpCheckedCells {
  const next = new Map([...checked].map(([name, paths]) => [name, new Set(paths)]));
  for (const serverName of serverNames) {
    const paths = new Set(next.get(serverName) ?? []);
    for (const pathId of pathIds) {
      if (selected) paths.add(pathId);
      else paths.delete(pathId);
    }
    next.set(serverName, paths);
  }
  return next;
}

// collectCheckedCells：勾选集合 → 同步入参（格子按 server、路径名字典序展开，
// 与后端的迭代口径一致，确认清单顺序稳定）。
export function collectCheckedCells(checked: McpCheckedCells): McpSyncCell[] {
  const cells: McpSyncCell[] = [];
  const serverNames = [...checked.keys()].sort();
  for (const serverName of serverNames) {
    for (const pathId of [...(checked.get(serverName) ?? [])].sort()) {
      cells.push({ serverName, pathId });
    }
  }
  return cells;
}

// excludeCells：从入参格子里剔除预演判定无法执行的格子（条目构造失败的
// 创建项）——确认清单里的就是将要执行的，二者严格一致（决策记录
// 2026-11-28-mcp-matrix-sync.md 第 3 条）。
export function excludeCells(cells: McpSyncCell[], excluded: McpSyncCell[]): McpSyncCell[] {
  const dropped = new Set(excluded.map((cell) => `${cell.serverName}\n${cell.pathId}`));
  return cells.filter((cell) => !dropped.has(`${cell.serverName}\n${cell.pathId}`));
}

export function countCheckedCells(checked: McpCheckedCells): number {
  let count = 0;
  for (const paths of checked.values()) count += paths.size;
  return count;
}
