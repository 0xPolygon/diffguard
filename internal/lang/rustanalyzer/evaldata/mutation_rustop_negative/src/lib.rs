// Uses Some(x) but tests don't distinguish Some from None — the test
// merely invokes the function without asserting the wrapped value, so
// the `some_to_none` mutant survives.

pub fn wrap(x: i32) -> Option<i32> {
    Some(x * 2)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn doesnt_panic() {
        // Invoking the function is all we check; the Option variant is
        // never inspected.
        let _ = wrap(5);
    }
}
