package runtime

import "time"

type coordinatorTimer interface {
	Stop() bool
}

type coordinatorClock interface {
	AfterFunc(time.Duration, func()) coordinatorTimer
}

type systemCoordinatorClock struct{}

func (systemCoordinatorClock) AfterFunc(delay time.Duration, callback func()) coordinatorTimer {
	return time.AfterFunc(delay, callback)
}
