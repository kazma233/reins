import type { CancellablePromise } from "@wailsio/runtime";

// 前后端类型的唯一来源是 wails bindings（frontend/bindings/），wrapper 与
// service 两侧结构化检查同型，不再需要调和两套生成物的断言桥。本文件保留
// 唯一的收口缝：service 调用返回的 CancellablePromise 收口成 wrapper 声明的
// Promise——Go 命令返回指针，wire 的 null 只与 error 同时出现（此时 Promise
// 已 reject），域保证非空的 wrapper 在这里收窄；可能合法返回 null 的（如
// 路径选择器用户取消）wrapper 保持 | null 原样透传。转换同时裁掉 cancel 面，
// 取消语义尚无前端消费方。

export function bindingsCall<T>(call: CancellablePromise<unknown>): Promise<T> {
  // CancellablePromise 自定义了 then 签名，与 Promise 结构化不兼容；这里做
  // 一次断言把调用值收口成 wrapper 声明的契约类型（见文件头注释）。
  return call as Promise<T>;
}
