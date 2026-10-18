package tree

import (
	"strconv"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// This file is the only place tree names the kernel's tree vocabulary: the
// event kinds and the Event field that holds a tree's name.

func openedEvent(name string) kernel.Event {
	return kernel.Event{Kind: kernel.EvTreeOpened, Tree: name}
}

func mergedEvent(name string, pr int) kernel.Event {
	return kernel.Event{Kind: kernel.EvTreeMerged, Tree: name, Detail: map[string]string{"pr": strconv.Itoa(pr)}}
}

// localMergedEvent is a merge that made a commit, not a pull request.
func localMergedEvent(name, sha string) kernel.Event {
	return kernel.Event{Kind: kernel.EvTreeMerged, Tree: name, Detail: map[string]string{"merge": sha}}
}

func checkEvent(name, result string) kernel.Event {
	return kernel.Event{
		Kind:   kernel.EvCIResult,
		Tree:   name,
		Detail: map[string]string{"result": result, "source": "local"},
	}
}
