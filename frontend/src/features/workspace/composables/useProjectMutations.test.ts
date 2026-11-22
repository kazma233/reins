import { beforeEach, expect, it, vi } from "vitest";
import { getWorkspaceState, selectProjectPath } from "../api";
import { useProjectMutations } from "./useProjectMutations";
import { useWorkspaceState } from "./useWorkspaceState";

vi.mock("../api", () => ({
  createWorkspaceProject: vi.fn(),
  deleteWorkspaceProject: vi.fn(),
  getWorkspaceState: vi.fn(),
  selectProjectPath: vi.fn(),
  updateWorkspaceProject: vi.fn(),
}));

const mockedSelectProjectPath = vi.mocked(selectProjectPath);

function setup() {
  const workspace = useWorkspaceState();
  const mutations = useProjectMutations(workspace);
  mutations.openProjectCreateDialog();
  return mutations;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getWorkspaceState).mockResolvedValue({
    document: null,
    inspection: null,
  });
});

it("选择 Windows 路径后自动取最后一级文件夹名作为 project id", async () => {
  // Windows 原生目录框返回反斜杠分隔路径，这里复现截图里的
  // `D:\projects\zhimareview`。
  mockedSelectProjectPath.mockResolvedValue({
    workspaceDir: "D:\\projects\\zhimareview",
  });
  const mutations = setup();

  await mutations.handlePickProjectPath();

  expect(mutations.projectCreateDialog.form).toMatchObject({
    path: "D:\\projects\\zhimareview",
    projectId: "zhimareview",
  });
});

it("选择 Unix 路径后同样取最后一级文件夹名", async () => {
  mockedSelectProjectPath.mockResolvedValue({
    workspaceDir: "/home/ly/zhimareview",
  });
  const mutations = setup();

  await mutations.handlePickProjectPath();

  expect(mutations.projectCreateDialog.form.projectId).toBe("zhimareview");
});

it("project id 已手填时不被选择路径覆盖", async () => {
  mockedSelectProjectPath.mockResolvedValue({
    workspaceDir: "D:\\projects\\zhimareview",
  });
  const mutations = setup();
  mutations.projectCreateDialog.form.projectId = "my-app";

  await mutations.handlePickProjectPath();

  expect(mutations.projectCreateDialog.form).toMatchObject({
    path: "D:\\projects\\zhimareview",
    projectId: "my-app",
  });
});

it("取消选择目录时不改动表单", async () => {
  mockedSelectProjectPath.mockResolvedValue(null);
  const mutations = setup();

  await mutations.handlePickProjectPath();

  expect(mutations.projectCreateDialog.form).toMatchObject({
    projectId: "",
    path: "",
  });
});
