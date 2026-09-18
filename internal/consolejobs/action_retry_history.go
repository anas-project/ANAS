package consolejobs

import (
	"errors"
	"sort"
)

// Snapshot job order is not execution order. Reconstruct each retry key's
// assignment history after the full snapshot is loaded, without trusting
// map iteration order or the snapshot digest as lifecycle evidence.
func (state *storeState) validateActionRetryHistory() error {
	type identity struct{ name, digest string }
	type owner struct {
		job     Job
		binding ActionRetryBinding
	}
	history := make(map[identity][]owner)
	for _, job := range state.jobs {
		if job.Action == nil || job.Action.Policy == nil {
			continue
		}
		for _, binding := range job.Action.Policy.RetryKeys {
			key := identity{job.Action.Name, binding.Digest}
			history[key] = append(history[key], owner{job: job, binding: binding})
		}
	}
	for _, owners := range history {
		sort.Slice(owners, func(left, right int) bool {
			return owners[left].binding.BoundAt.Before(owners[right].binding.BoundAt)
		})
		for index := 1; index < len(owners); index++ {
			previous, next := owners[index-1], owners[index]
			if previous.job.FinishedAt == nil || !previous.job.Status.terminal() ||
				!next.binding.BoundAt.After(previous.binding.BoundAt) ||
				next.binding.BoundAt.Before(previous.job.FinishedAt.Add(ActionKeyRetention)) {
				return errors.New("action retry history contains overlapping or ambiguous owners")
			}
		}
	}
	return nil
}
