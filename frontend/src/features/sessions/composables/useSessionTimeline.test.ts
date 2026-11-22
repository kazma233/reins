import { describe, expect, it, vi } from "vitest";
import { ref } from "vue";
import { createTimelineLoader } from "./useSessionTimeline";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("createTimelineLoader", () => {
  it("no-ops when the request key is null (no detail selected)", async () => {
    const fetchAll = vi.fn(async () => [1]);
    const loader = createTimelineLoader(() => null, fetchAll, "加载失败。");

    await loader.load();

    expect(fetchAll).not.toHaveBeenCalled();
    expect(loader.loading.value).toBe(false);
    expect(loader.items.value).toEqual([]);
  });

  it("drops a second load while one is in flight (concurrency guard)", async () => {
    const gate = deferred<number[]>();
    const fetchAll = vi.fn(() => gate.promise);
    const loader = createTimelineLoader(() => "k", fetchAll, "加载失败。");

    void loader.load();
    void loader.load();

    expect(fetchAll).toHaveBeenCalledTimes(1);
    expect(loader.loading.value).toBe(true);

    gate.resolve([1]);
    await Promise.resolve();
    expect(loader.loading.value).toBe(false);
  });

  it("drops the fetched items when the identity changed mid-flight", async () => {
    const key = ref<string | null>("codex:a");
    const gate = deferred<number[]>();
    const loader = createTimelineLoader(() => key.value, () => gate.promise, "加载失败。");

    void loader.load();
    key.value = "claude_code:b";
    gate.resolve([1, 2]);
    await Promise.resolve();

    // 旧会话的数据不落进新会话的落点；loading 也不在此复位——清场归 reset
    //（真实流程里 overview 变化的 watch 会先 reset 再加载）。
    expect(loader.items.value).toEqual([]);
    expect(loader.loading.value).toBe(true);
  });

  it("records the error only when the identity still matches", async () => {
    const key = ref<string | null>("codex:a");
    const gate = deferred<number[]>();
    const loader = createTimelineLoader(() => key.value, () => gate.promise, "加载时间线失败。");

    void loader.load();
    key.value = "claude_code:b";
    gate.reject(new Error("boom"));
    await Promise.resolve();
    expect(loader.error.value).toBeNull();

    const steady = deferred<number[]>();
    const steadyLoader = createTimelineLoader(() => "k", () => steady.promise, "加载时间线失败。");
    void steadyLoader.load();
    steady.reject(new Error("boom"));
    await Promise.resolve();
    expect(steadyLoader.error.value).toBe("boom");
  });

  it("reset clears items, loading and error", () => {
    const loader = createTimelineLoader(() => "k", async () => [1], "加载失败。");
    loader.items.value = [1];
    loader.loading.value = true;
    loader.error.value = "旧错误";

    loader.reset();

    expect(loader.items.value).toEqual([]);
    expect(loader.loading.value).toBe(false);
    expect(loader.error.value).toBeNull();
  });
});
