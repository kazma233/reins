// skill 来源 git 拉取的取消槽：同一时刻只有一个在途拉取（弹窗 busy 串行
// 保证），所以单槽足够。begin() 每次换新标记，被换下的旧拉取自然不可取消；
// cancel() 只置位当前标记，没有在途拉取时是无害的空操作。
use std::sync::atomic::AtomicBool;
use std::sync::{Arc, Mutex};

use crate::support::cancel::CancelCheck;

#[derive(Default)]
pub(crate) struct SkillSourceFetchCancelState {
    current: Mutex<Option<Arc<AtomicBool>>>,
}

impl SkillSourceFetchCancelState {
    pub(crate) fn begin(&self) -> CancelCheck {
        let flag = Arc::new(AtomicBool::new(false));
        *self
            .current
            .lock()
            .expect("skill source fetch cancel state poisoned") = Some(flag.clone());
        CancelCheck::from_flag(flag)
    }

    pub(crate) fn cancel(&self) {
        if let Some(flag) = self
            .current
            .lock()
            .expect("skill source fetch cancel state poisoned")
            .as_ref()
        {
            flag.store(true, std::sync::atomic::Ordering::Relaxed);
        }
    }
}
