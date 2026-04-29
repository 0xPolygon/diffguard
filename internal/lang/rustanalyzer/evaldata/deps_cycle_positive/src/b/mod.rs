use crate::a::a_fn;

pub fn b_fn(x: i32) -> i32 {
    if x > 100 { x } else { a_fn(x - 1) }
}
