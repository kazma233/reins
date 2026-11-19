// app 外壳的后端调用面：与 features/<域>/api.ts 同构的唯一入口（AGENTS.md
// 契约规则），外壳自身的后端调用只经这里，不直接 import @bindings。
import { bindingsCall } from "@shared/lib/bindings";
import { AppInfoService } from "@bindings/reins";

export function appVersion(): Promise<string> {
  return bindingsCall(AppInfoService.Version());
}
