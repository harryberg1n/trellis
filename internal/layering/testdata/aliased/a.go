package aliased

import (
	clock "time"
)

func Wait() { clock.Sleep(clock.Second) }

type time struct{}

func (time) Now() int { return 0 }

func Shadow() int {
	var time time
	return time.Now()
}
