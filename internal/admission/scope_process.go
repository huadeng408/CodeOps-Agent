package admission

import "code-agent/internal/safety"

// The adapter preserves historical scope JSON while process checks are shared.
type scopeProcess safety.ProcessIdentity

type recoveredScope struct {
	ID      string       `json:"id"`
	Process scopeProcess `json:"process"`
}

func scopeHost() (string, error) { return safety.ProcessHost() }
func currentScopeProcess() (*scopeProcess, error) {
	process, err := safety.CurrentProcessIdentity()
	return (*scopeProcess)(process), err
}
func (process scopeProcess) valid() bool { return safety.ProcessIdentity(process).Valid() }
func (process scopeProcess) confirmedStopped() bool {
	return safety.ProcessIdentity(process).ConfirmedStopped()
}
