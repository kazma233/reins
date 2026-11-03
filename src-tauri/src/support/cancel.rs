// 协作式取消的两个原语：
//
// - `CancelCheck`：长操作在安全边界轮询的检查。闭包形态让测试能注入
//   「第 N 次检查后置位」的脚本；生产实现只是读一个 AtomicBool。
// - `Cancelled`：操作被取消的标记错误。中间层用 `?` 透传，只有决定
//   「取消之后怎么办」的层用 `is::<Cancelled>()` 识别，不做字符串匹配。

use std::sync::Arc;

#[derive(Clone)]
pub(crate) struct CancelCheck(Arc<dyn Fn() -> bool + Send + Sync>);

impl CancelCheck {
    // 不参与取消的调用方传它：没有任何入口能置位，轮询恒为否。
    pub(crate) fn never() -> Self {
        Self(Arc::new(|| false))
    }

    pub(crate) fn from_flag(flag: Arc<std::sync::atomic::AtomicBool>) -> Self {
        use std::sync::atomic::Ordering;
        Self(Arc::new(move || flag.load(Ordering::Relaxed)))
    }

    // 测试脚本入口：任意谓词都能当检查点。
    #[cfg(test)]
    pub(crate) fn scripted(check: impl Fn() -> bool + Send + Sync + 'static) -> Self {
        Self(Arc::new(check))
    }

    pub(crate) fn is_cancelled(&self) -> bool {
        (self.0)()
    }
}

// 裸错误形态保证 `err.is::<Cancelled>()` 在跨层 `?`/context 后仍可识别；
// 面向用户的说明由决定取消语义的层加 context。
#[derive(Debug)]
pub(crate) struct Cancelled;

impl std::fmt::Display for Cancelled {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "操作已取消。")
    }
}

impl std::error::Error for Cancelled {}
