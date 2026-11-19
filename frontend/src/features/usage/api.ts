import { bindingsCall } from "@shared/lib/bindings";
import { SessionService } from "@bindings/reins/internal/session";
import type { UsageStats } from "./types";

// 会话统计页的后端实现在 session 域（usage_stats），bindings 也挂在
// SessionService 上；收口说明见 @shared/lib/bindings。

export function getUsageStats(): Promise<UsageStats> {
  return bindingsCall(SessionService.GetUsageStats());
}
