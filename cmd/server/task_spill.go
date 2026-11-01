package main

import "code-agent/internal/tools"

func newTaskSpillStore(storage, namespace string) (tools.SpillStore, error) {
	return tools.NewTaskSpillStore(storage, namespace)
}
