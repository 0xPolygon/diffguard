// Rust counterpart: a deliberately complex function whose cognitive
// complexity should exceed the threshold.

pub fn bad_rust(a: i32, b: i32, c: i32, d: i32, e: i32) -> i32 {
    let mut total = 0;
    if a > 0 && b > 0 {
        if c > 0 || d > 0 {
            for i in 0..a {
                if i % 2 == 0 && e > 0 {
                    total += i;
                } else if i % 3 == 0 || e < 0 {
                    total -= i;
                }
            }
        }
    }
    match a {
        1 => total += 1,
        2 => total += 2,
        3 => total += 3,
        _ => total += 0,
    }
    total
}
