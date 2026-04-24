// Fixture: a small Rust file covering every function form the extractor
// should handle: standalone fn, inherent method, trait-impl method, and
// nested functions (reported as separate entries).

fn standalone() -> i32 {
    42
}

pub struct Counter {
    n: i32,
}

impl Counter {
    pub fn new() -> Self {
        Counter { n: 0 }
    }

    pub fn increment(&mut self) -> i32 {
        fn nested_helper(x: i32) -> i32 {
            x + 1
        }
        self.n = nested_helper(self.n);
        self.n
    }
}

pub trait Named {
    fn name(&self) -> &str;
}

impl Named for Counter {
    fn name(&self) -> &str {
        "Counter"
    }
}
