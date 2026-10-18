package tree

import (
	"strconv"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// This file is the only place tree names the kernel's lane vocabulary: the
// event kinds and the Event field that holds a tree's name.

func openedEvent(name string) kernel.Event {
	return kernel.Event{Kind: kernel.EvLaneOpened, Lane: name}
}

func mergedEvent(name string, pr int) kernel.Event {
	return kernel.Event{Kind: kernel.EvLaneMerged, Lane: name, Detail: map[string]string{"pr": strconv.Itoa(pr)}}
}

func checkEvent(name, result string) kernel.Event {
	return kernel.Event{
		Kind:   kernel.EvCIResult,
		Lane:   name,
		Detail: map[string]string{"result": result, "source": "local"},
	}
}
