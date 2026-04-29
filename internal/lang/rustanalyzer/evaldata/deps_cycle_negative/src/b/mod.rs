use crate::types::Shared;

pub fn b_fn(x: i32) -> Shared {
    Shared { value: x + 2 }
}
