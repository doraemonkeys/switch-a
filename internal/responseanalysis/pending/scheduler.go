package pending

import "time"

type Timer interface {
	Stop() bool
}

// Scheduler is consumed here rather than produced by a clock package so tests
// can linearize timer delivery without wall-clock sleeps.
type Scheduler interface {
	AfterFunc(time.Duration, func()) Timer
}

type RealScheduler struct{}

func (RealScheduler) AfterFunc(delay time.Duration, callback func()) Timer {
	return time.AfterFunc(delay, callback)
}
