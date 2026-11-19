// 会话域契约类型的唯一来源是 wails bindings（frontend/bindings/，经
// `wails3 generate bindings` 从 Go 侧生成）；本文件只 re-export，出口名与
// 旧 ts-rs generated barrel 保持一致，域内 import 不用改动。SourceApp 是
// 枚举值（与 agents 清单共用同一键类型），需要成员比较的调用方按值导入。

export type {
  ContentBlock,
  SessionAgent,
  SessionEvent,
  SessionMessage,
  SessionModel,
  SessionOverview,
  SessionPage,
  SessionRefreshResult,
  SessionSummary,
  SessionTokenUsage,
  SourceStatus,
} from "@bindings/reins/internal/session/models";

export { SourceApp } from "@bindings/reins/internal/agents/models";
