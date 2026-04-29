// Rust fails: tangled complexity > 10.

pub fn tangled(a: Option<i32>, b: Option<i32>, flag: bool) -> i32 {
    let mut total = 0;
    if let Some(x) = a {
        if x > 0 && flag {
            if let Some(y) = b {
                match y {
                    v if v > 100 && x < 10 => total += v + x,
                    v if v < 0 || x == 0 => total -= v,
                    _ => total += 1,
                }
            }
        } else {
            match x {
                1 => total = 1,
                2 => total = 2,
                3 => total = 3,
                _ => total = -1,
            }
        }
    }
    total
}
