import { beforeEach, describe, expect, it, vi } from "vitest";
import { McpPathState, McpTransport } from "../types";
import { getWorkspaceState, updateWorkspaceMcp } from "../api";
import { useMcpMutations } from "./useMcpMutations";
import { useWorkspaceState } from "./useWorkspaceState";

vi.mock("../api", () => ({
  createWorkspaceMcp: vi.fn(),
  deleteWorkspaceMcp: vi.fn(),
  getWorkspaceState: vi.fn(),
  updateWorkspaceMcp: vi.fn(),
}));

const mockedGetWorkspaceState = vi.mocked(getWorkspaceState);
const mockedUpdateWorkspaceMcp = vi.mocked(updateWorkspaceMcp);

// 编辑已有 mcp 的前提：检查结果里该 mcp 已安装到 codex。
function inspectionWithInstalledPath() {
  return {
    configPath: "/tmp/config.yaml",
    skillPaths: [],
    warnings: [],
    mcps: [
      {
        name: "filesystem",
        paths: [
          {
            pathId: "codex",
            state: McpPathState.PathStatePresent,
            detail: "",
          },
        ],
      },
    ],
  };
}

function setup() {
  const workspace = useWorkspaceState();
  workspace.setInspection(inspectionWithInstalledPath());
  const mutations = useMcpMutations(workspace);
  return { workspace, mutations };
}

function openExistingMcp(mutations: ReturnType<typeof useMcpMutations>) {
  mutations.openMcpEditDialog({
    name: "filesystem",
    transport: McpTransport.TransportStdio,
    createdAt: 1,
    homepage: null,
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-filesystem"],
    env: {},
    url: null,
    headers: {},
    timeout: null,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockedGetWorkspaceState.mockResolvedValue({ document: null, inspection: null });
  mockedUpdateWorkspaceMcp.mockResolvedValue({
    serverName: "filesystem",
    updatedPaths: [],
  });
});

it("saves directly without the rename dialog when the name is unchanged", async () => {
  const { mutations } = setup();
  openExistingMcp(mutations);
  mutations.mcpCreateDialog.form.command = "uvx";

  await mutations.handleCreateWorkspaceMcp();

  expect(mutations.mcpSyncConfirmDialog.open).toBe(false);
  expect(mockedUpdateWorkspaceMcp).toHaveBeenCalledWith(
    "filesystem",
    expect.objectContaining({ name: "filesystem", command: "uvx" }),
  );
  expect(mutations.mcpCreateDialog.open).toBe(false);
});

it("asks to sync installed targets only when the name actually changes", async () => {
  const { mutations } = setup();
  openExistingMcp(mutations);
  mutations.mcpCreateDialog.form.name = "filesystem-renamed";

  await mutations.handleCreateWorkspaceMcp();

  expect(mutations.mcpSyncConfirmDialog).toMatchObject({
    open: true,
    originalName: "filesystem",
    nextName: "filesystem-renamed",
    pathIds: ["codex"],
  });
  // 确认前不能落库：用户取消时不应产生半套写入。
  expect(mockedUpdateWorkspaceMcp).not.toHaveBeenCalled();
});

it("saves a renamed mcp directly when it is not installed anywhere", async () => {
  const { workspace, mutations } = setup();
  workspace.setInspection({ ...inspectionWithInstalledPath(), mcps: [] });
  openExistingMcp(mutations);
  mutations.mcpCreateDialog.form.name = "filesystem-renamed";

  await mutations.handleCreateWorkspaceMcp();

  expect(mutations.mcpSyncConfirmDialog.open).toBe(false);
  expect(mockedUpdateWorkspaceMcp).toHaveBeenCalledWith(
    "filesystem",
    expect.objectContaining({ name: "filesystem-renamed" }),
  );
});

describe("确认重命名并同步", () => {
  async function openRenameConfirm() {
    const { mutations } = setup();
    openExistingMcp(mutations);
    mutations.mcpCreateDialog.form.name = "filesystem-renamed";
    await mutations.handleCreateWorkspaceMcp();
    return { mutations };
  }

  it("sends a single update: the backend rewrites installed paths inside one command", async () => {
    const { mutations } = await openRenameConfirm();

    await mutations.handleConfirmSyncMcpConfig();

    // 改名收敛在后端锁内完成，前端不再逐路径串行调用。
    expect(mockedUpdateWorkspaceMcp).toHaveBeenCalledTimes(1);
    expect(mockedUpdateWorkspaceMcp).toHaveBeenCalledWith(
      "filesystem",
      expect.objectContaining({ name: "filesystem-renamed" }),
    );
    expect(mutations.mcpSyncConfirmDialog.open).toBe(false);
    expect(mutations.mcpCreateDialog.open).toBe(false);
  });

  it("keeps the confirm dialog open with the error when the backend rejects", async () => {
    mockedUpdateWorkspaceMcp.mockRejectedValue(new Error("rename boom"));
    const { mutations } = await openRenameConfirm();

    await mutations.handleConfirmSyncMcpConfig();

    expect(mutations.mcpSyncConfirmDialog).toMatchObject({
      open: true,
      error: "rename boom",
    });
    expect(mutations.mcpCreateDialog.open).toBe(true);
  });
});
