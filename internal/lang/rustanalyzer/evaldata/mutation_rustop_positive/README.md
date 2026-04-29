# mutation_rustop_positive

Exercises the Rust-specific `unwrap_removal` operator. `double(opt)`
uses `.unwrap()` on an `Option<i32>`; removing the call breaks types, so
cargo build fails and the mutant is killed.

Expected verdict: Mutation Testing PASS — at least one unwrap_removal
mutant is generated and killed.

Requires `cargo` on PATH — eval_test.go skips cleanly when absent.
