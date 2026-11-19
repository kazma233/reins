import { describe, expect, it } from "vitest";
import { AGENT_ID_ALIASES, AGENT_LABELS } from "@shared/lib/agent-labels";
import { SourceApp } from "./types";
import { formatSourceAppName } from "./source-app";

// 来源名与 target / providers 共用同一份常量：新增来源时若忘了登记产品名，
// 这里会立刻失败，避免界面回退成裸 id。
const SOURCE_APPS: SourceApp[] = [
  SourceApp.SourceCodex,
  SourceApp.SourceClaudeCode,
  SourceApp.SourceOpenCode,
  SourceApp.SourcePi,
  SourceApp.SourceGrokBuild,
  SourceApp.SourceZcode,
  SourceApp.SourceDsh,
];

describe("formatSourceAppName", () => {
  it("renders every known source with a canonical name, never the raw id", () => {
    for (const app of SOURCE_APPS) {
      const label = formatSourceAppName(app);
      const canonicalId = AGENT_ID_ALIASES[app] ?? app;
      expect(label).toBe(AGENT_LABELS[canonicalId]);
      expect(label).not.toBe(app);
    }
  });
});
