package durationonly

import "time"

const Window = 5 * time.Second

func Add(a, b time.Duration) time.Duration { return a + b }
