// Uses .unwrap() in a well-tested way: the test asserts both the Some
// (happy) path and constructs the expected value after unwrap. Removing
// .unwrap() breaks the type signature and the test fails, killing the
// mutant.

pub fn double(opt: Option<i32>) -> i32 {
    let x = opt.unwrap();
    x * 2
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn doubles_the_value() {
        assert_eq!(double(Some(5)), 10);
    }

    #[test]
    fn doubles_zero() {
        assert_eq!(double(Some(0)), 0);
    }
}
