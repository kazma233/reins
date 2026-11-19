import { describe, expect, it } from "vitest";
import { SourceApp } from "./types";
import { formatSourceAppName } from "./source-app";

// 这里只钉来源产品名。
describe("Grok Build session contract", () => {
  it("labels the Grok Build source", () => {
    expect(formatSourceAppName(SourceApp.SourceGrokBuild)).toBe("Grok Build");
  });
});

describe("Pi session contract", () => {
  it("labels the Pi source", () => {
    expect(formatSourceAppName(SourceApp.SourcePi)).toBe("Pi");
  });
});

describe("DSH session contract", () => {
  it("labels the DSH source", () => {
    expect(formatSourceAppName(SourceApp.SourceDsh)).toBe("DeepSeek Harness");
  });
});
