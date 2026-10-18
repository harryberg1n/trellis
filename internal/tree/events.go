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

// mergedEvent is a merged pull request. sha is the commit it landed as, when
// GitHub named one; the report links the history to the tree by it.
func mergedEvent(name string, pr int, sha string) kernel.Event {
	detail := map[string]string{kernel.DetailPR: strconv.Itoa(pr)}
	if sha != "" {
		detail[kernel.DetailMerge] = sha
	}
	return kernel.Event{Kind: kernel.EvTreeMerged, Tree: name, Detail: detail}
}

// localMergedEvent is a merge that made a commit, not a pull request.
func localMergedEvent(name, sha string) kernel.Event {
	return kernel.Event{Kind: kernel.EvTreeMerged, Tree: name, Detail: map[string]string{kernel.DetailMerge: sha}}
}

func checkEvent(name, result string) kernel.Event {
	return kernel.Event{
		Kind:   kernel.EvCIResult,
		Tree:   name,
		Detail: map[string]string{"result": result, "source": "local"},
	}
}
