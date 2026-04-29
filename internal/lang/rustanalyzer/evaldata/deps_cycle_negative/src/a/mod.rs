use crate::types::Shared;

pub fn a_fn(x: i32) -> Shared {
    Shared { value: x + 1 }
}
