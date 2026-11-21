package mutation

import (
	"reflect"
	"testing"
)

func numbered(n int, operator string) []Mutant {
	mutants := make([]Mutant, n)
	for i := range mutants {
		mutants[i] = Mutant{File: "a.go", Line: i + 1, Operator: operator}
	}
	return mutants
}

func TestDedupeMutants(t *testing.T) {
	a := Mutant{File: "a.go", Line: 3, Offset: 40, Operator: "negate_conditional", Description: "== -> !="}
	sameLineOtherSite := a
	sameLineOtherSite.Offset = 52
	got := dedupeMutants([]Mutant{a, sameLineOtherSite, a})
	if len(got) != 2 {
		t.Fatalf("got %d mutants, want 2: an exact duplicate is dropped, a second site on the line is kept", len(got))
	}
	if got[0].Offset != 40 || got[1].Offset != 52 {
		t.Errorf("order not preserved: %+v", got)
	}
}

func TestShardMutants_PartitionsTheList(t *testing.T) {
	mutants := numbered(10, "negate_conditional")
	seen := map[int]int{}
	total := 0
	for index := 1; index <= 3; index++ {
		shard := shardMutants(mutants, index, 3)
		total += len(shard)
		for _, m := range shard {
			seen[m.Line]++
		}
	}
	if total != 10 || len(seen) != 10 {
		t.Fatalf("3 shards covered %d mutants (%d distinct), want all 10 exactly once", total, len(seen))
	}
	if got := shardMutants(mutants, 2, 3); got[0].Line != 2 || got[1].Line != 5 {
		t.Errorf("shard 2/3 starts %d,%d; want lines 2,5", got[0].Line, got[1].Line)
	}
}

func TestShardMutants_DisabledOrInvalid(t *testing.T) {
	mutants := numbered(4, "negate_conditional")
	for _, c := range [][2]int{{0, 0}, {1, 1}, {0, 3}, {4, 3}} {
		if got := shardMutants(mutants, c[0], c[1]); len(got) != 4 {
			t.Errorf("shard %d/%d kept %d mutants, want all 4", c[0], c[1], len(got))
		}
	}
}

func TestPlanMutants_SameSeedSameSample(t *testing.T) {
	sample := func(seed int64) []Mutant {
		return planMutants(numbered(50, "x"), Options{SampleRate: 20, Seed: seed})
	}
	a, b, c := sample(7), sample(7), sample(8)
	if !reflect.DeepEqual(a, b) {
		t.Error("the same seed must select the same mutants")
	}
	if reflect.DeepEqual(a, c) {
		t.Error("a different seed should select different mutants")
	}
	if len(a) != 10 {
		t.Errorf("20%% of 50 = %d, want 10", len(a))
	}
	if got := sampleMutants(numbered(3, "x"), 1); len(got) != 1 {
		t.Errorf("a sample never rounds down to zero mutants, got %d", len(got))
	}
}

func TestPlanMutants_GatingTiersFirst(t *testing.T) {
	mutants := []Mutant{
		{File: "a.go", Line: 1, Operator: "statement_deletion"},
		{File: "a.go", Line: 2, Operator: "boolean_substitution"},
		{File: "a.go", Line: 3, Operator: "negate_conditional"},
		{File: "a.go", Line: 4, Operator: "statement_deletion"},
		{File: "a.go", Line: 5, Operator: "negate_conditional"},
	}
	got := planMutants(mutants, Options{SampleRate: 100})
	var lines []int
	for _, m := range got {
		lines = append(lines, m.Line)
	}
	if want := []int{3, 5, 2, 1, 4}; !reflect.DeepEqual(lines, want) {
		t.Errorf("order = %v, want %v (tier 1, tier 2, tier 3; source order within a tier)", lines, want)
	}
}

func TestPlanMutants_AppliesShardSampleAndShuffle(t *testing.T) {
	if got := planMutants(numbered(40, "negate_conditional"), Options{SampleRate: 100, ShardIndex: 1, ShardCount: 4}); len(got) != 10 {
		t.Errorf("shard 1/4 of 40 = %d, want 10", len(got))
	}
	if got := planMutants(numbered(40, "negate_conditional"), Options{SampleRate: 50}); len(got) != 20 {
		t.Errorf("50%% sample of 40 = %d, want 20", len(got))
	}
	plain := planMutants(numbered(40, "negate_conditional"), Options{SampleRate: 100})
	shuffled := planMutants(numbered(40, "negate_conditional"), Options{SampleRate: 100, Confidence: 0.95})
	if plain[0].Line != 1 || plain[39].Line != 40 {
		t.Error("without early stopping, source order is kept")
	}
	if reflect.DeepEqual(plain, shuffled) {
		t.Error("early stopping needs a shuffled order so a prefix is a random sample")
	}
}

func TestShardMutants_TwoShards(t *testing.T) {
	mutants := numbered(5, "x")
	if got := shardMutants(mutants, 1, 2); len(got) != 3 {
		t.Errorf("shard 1/2 of 5 = %d, want 3", len(got))
	}
	if got := shardMutants(mutants, 2, 2); len(got) != 2 {
		t.Errorf("shard 2/2 of 5 = %d, want 2", len(got))
	}
}

func TestSampleMutants_Bounds(t *testing.T) {
	if got := sampleMutants(nil, 20); len(got) != 0 {
		t.Errorf("sampling nothing yields nothing, got %d", len(got))
	}
	if got := sampleMutants(numbered(7, "x"), 100); len(got) != 7 {
		t.Errorf("100%% keeps everything, got %d", len(got))
	}
	if got := sampleMutants(numbered(7, "x"), 250); len(got) != 7 {
		t.Errorf("a rate above 100 keeps everything, got %d", len(got))
	}
	if got := planMutants(nil, Options{SampleRate: 20}); len(got) != 0 {
		t.Errorf("planning an empty list yields nothing, got %d", len(got))
	}
}
