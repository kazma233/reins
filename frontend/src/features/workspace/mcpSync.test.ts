import { describe, expect, it } from "vitest";
import { McpPathState, McpTransport } from "./types";
import type { McpConfigView, McpPathView, WorkspaceInspection } from "./types";
import {
  buildMcpCellStates,
  cellDisabledReason,
  collectCheckedCells,
  countCheckedCells,
  defaultCheckedCells,
  excludeCells,
  isCellChecked,
  isCellSelectable,
  setCellsChecked,
  toggleCell,
} from "./mcpSync";

function inspectionWith(states: Array<[string, string, McpPathState]>): WorkspaceInspection {
  const byServer = new Map<string, Array<{ pathId: string; state: McpPathState; detail: string }>>();
  for (const [server, path, state] of states) {
    const list = byServer.get(server) ?? [];
    list.push({ pathId: path, state, detail: `detail-${server}-${path}` });
    byServer.set(server, list);
  }
  return {
    configPath: "/tmp/config.yaml",
    skillPaths: [],
    warnings: [],
    mcps: [...byServer].map(([name, paths]) => ({ name, paths })),
  };
}

function serverView(name: string, transport: McpTransport): McpConfigView {
  return {
    name,
    transport,
    createdAt: 1,
    homepage: null,
    command: null,
    args: [],
    env: {},
    url: null,
    headers: {},
    timeout: null,
  };
}

function pathView(name: string, sseUnsupported = false): McpPathView {
  return {
    name,
    configPath: `/tmp/${name}.json`,
    configPrefix: "mcpServers",
    mcpFormatDescription: "",
    mcpFormatExamples: [],
    sseUnsupported,
  };
}

const servers = [serverView("alpha", McpTransport.TransportStdio), serverView("pi-remote", McpTransport.TransportSSE)];
// pi 条目模拟「配置文件属于 pi 的 mcp.json」：SSE 防呆由后端按配置文件判定
// 下发，前端只认标记不认名字。
const paths = [pathView("claude"), pathView("pi", true), pathView("zcode")];

describe("格子可选性", () => {
  it("error 格不可勾，SSE + 不兼容工具的格子禁用", () => {
    const states = buildMcpCellStates(
      inspectionWith([["alpha", "claude", McpPathState.PathStateError]]),
    );
    const claude = states.get("alpha")?.get("claude");
    expect(isCellSelectable(McpTransport.TransportStdio, false, claude)).toBe(false);
    expect(cellDisabledReason(McpTransport.TransportStdio, false, claude)).toBe(
      "detail-alpha-claude",
    );
    // SSE + 标记的配置文件：知识由路径视图下发，与条目名无关。
    expect(isCellSelectable(McpTransport.TransportSSE, true, null)).toBe(false);
    expect(isCellSelectable(McpTransport.TransportSSE, false, null)).toBe(true);
    expect(cellDisabledReason(McpTransport.TransportSSE, true, null)).toBe(
      "该工具不接受 SSE transport 的 MCP。",
    );
  });
});

describe("默认勾选 = 已装且可选的格子", () => {
  it("present 全勾，missing/error 不勾，不可选格不勾", () => {
    const cellStates = buildMcpCellStates(
      inspectionWith([
        ["alpha", "claude", McpPathState.PathStatePresent],
        ["alpha", "zcode", McpPathState.PathStateMissing],
        ["alpha", "pi", McpPathState.PathStatePresent],
        ["pi-remote", "pi", McpPathState.PathStatePresent],
      ]),
    );
    const checked = defaultCheckedCells(servers, paths, cellStates);
    expect(isCellChecked(checked, "alpha", "claude")).toBe(true);
    expect(isCellChecked(checked, "alpha", "zcode")).toBe(false);
    expect(isCellChecked(checked, "alpha", "pi")).toBe(true); // stdio 不受 SSE 防呆影响
    expect(isCellChecked(checked, "pi-remote", "pi")).toBe(false); // SSE + 标记的配置文件
    expect(countCheckedCells(checked)).toBe(2);
  });
});

describe("勾选集合操作", () => {
  it("toggleCell 返回新集合，不改原集合", () => {
    const checked = new Map([["alpha", new Set(["claude"])]]);
    const next = toggleCell(checked, "alpha", "zcode");
    expect(isCellChecked(next, "alpha", "zcode")).toBe(true);
    expect(isCellChecked(checked, "alpha", "zcode")).toBe(false);
    const undone = toggleCell(next, "alpha", "zcode");
    expect(isCellChecked(undone, "alpha", "zcode")).toBe(false);
  });

  it("setCellsChecked 批量作用于传入组合", () => {
    const checked = defaultCheckedCells(
      servers,
      paths,
      buildMcpCellStates(inspectionWith([["alpha", "claude", McpPathState.PathStatePresent]])),
    );
    const selected = setCellsChecked(checked, ["alpha"], ["zcode", "pi"], true);
    expect(countCheckedCells(selected)).toBe(3);
    const cleared = setCellsChecked(selected, ["alpha"], ["claude", "zcode", "pi"], false);
    expect(countCheckedCells(cleared)).toBe(0);
  });
});

describe("collectCheckedCells 展开为同步入参", () => {
  it("按 server、路径名字典序展开", () => {
    const checked = new Map([
      ["zeta", new Set(["b-path", "a-path"])],
      ["alpha", new Set(["c-path"])],
    ]);
    expect(collectCheckedCells(checked)).toEqual([
      { serverName: "alpha", pathId: "c-path" },
      { serverName: "zeta", pathId: "a-path" },
      { serverName: "zeta", pathId: "b-path" },
    ]);
  });
});

describe("excludeCells 剔除预演失败格", () => {
  it("按 server + path 剔除，其余保持原序", () => {
    const cells = [
      { serverName: "alpha", pathId: "a" },
      { serverName: "alpha", pathId: "b" },
      { serverName: "zeta", pathId: "a" },
    ];
    expect(excludeCells(cells, [{ serverName: "alpha", pathId: "b" }])).toEqual([
      { serverName: "alpha", pathId: "a" },
      { serverName: "zeta", pathId: "a" },
    ]);
  });
});
