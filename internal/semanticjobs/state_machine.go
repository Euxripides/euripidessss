package semanticjobs

import "fmt"

var validTransitions = map[Status]map[Status]struct{}{
	StatusQueued: {
		StatusRunning: {}, StatusCancelled: {},
	},
	StatusRunning: {
		StatusQueued:    {}, // process restart recovery only
		StatusCompleted: {}, StatusFailed: {}, StatusCancelled: {},
	},
	StatusFailed: {
		StatusQueued: {}, // explicit Retry only
	},
	StatusCompleted: {},
	StatusCancelled: {},
}

func CanTransition(from, to Status) bool {
	_, ok := validTransitions[from][to]
	return ok
}

func transition(job *Job, target Status) error {
	if !CanTransition(job.Status, target) {
		return fmt.Errorf("invalid semantic job transition %s -> %s", job.Status, target)
	}
	job.Status = target
	return nil
}
