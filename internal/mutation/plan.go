package mutation

import (
	"math/rand"
	"sort"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// planMutants turns the raw generated list into the ordered work list:
// duplicates dropped, the shard and sample applied, and gating tiers first
// so a budget or cancellation costs the least important results.
func planMutants(mutants []Mutant, opts Options) []Mutant {
	mutants = dedupeMutants(mutants)
	mutants = shardMutants(mutants, opts.ShardIndex, opts.ShardCount)
	// Sampling keeps a random subset; early stopping reads a prefix of each
	// tier as a random sample. Both need the same seeded shuffle.
	if opts.SampleRate < 100 || opts.Confidence > 0 {
		shuffleMutants(mutants, opts.seed())
	}
	mutants = sampleMutants(mutants, opts.SampleRate)
	sort.SliceStable(mutants, func(i, j int) bool {
		return operatorTier(mutants[i].Operator) < operatorTier(mutants[j].Operator)
	})
	return mutants
}

// dedupeMutants drops mutants identical in every identifying field: testing
// the same mutation twice costs a run and double-counts its result.
func dedupeMutants(mutants []Mutant) []Mutant {
	seen := make(map[lang.MutantSite]bool, len(mutants))
	out := mutants[:0:0]
	for _, m := range mutants {
		if seen[m.site()] {
			continue
		}
		seen[m.site()] = true
		out = append(out, m)
	}
	return out
}

// shardMutants keeps every count-th mutant starting at the 1-based index,
// so `count` jobs with indexes 1..count partition the list.
func shardMutants(mutants []Mutant, index, count int) []Mutant {
	if count < 2 || index < 1 || index > count {
		return mutants
	}
	var out []Mutant
	for i := index - 1; i < len(mutants); i += count {
		out = append(out, mutants[i])
	}
	return out
}

func shuffleMutants(mutants []Mutant, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(mutants), func(i, j int) {
		mutants[i], mutants[j] = mutants[j], mutants[i]
	})
}

// sampleMutants keeps the first rate percent of an already shuffled list,
// and never fewer than one mutant of a non-empty list.
func sampleMutants(mutants []Mutant, rate float64) []Mutant {
	n := max(1, int(float64(len(mutants))*rate/100))
	return mutants[:min(n, len(mutants))]
}
