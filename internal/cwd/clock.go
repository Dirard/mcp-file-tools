package cwd

import "time"

// Clock makes absolute registry expiry deterministic without a background worker.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}
