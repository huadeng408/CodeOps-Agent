package recovery

import "strings"

type Strategy string

const (
	StrategyRetrySame   Strategy = "retry_same"
	StrategySwitchModel Strategy = "switch_model"
	StrategyAskUser     Strategy = "ask_user"
	StrategyAbort       Strategy = "abort"
)

type Engine struct{}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Choose(errorCount int, lastError string) Strategy {
	lower := strings.ToLower(lastError)
	switch {
	case strings.Contains(lower, "permission"):
		return StrategyAskUser
	case errorCount >= 2:
		return StrategySwitchModel
	case errorCount == 1:
		return StrategyRetrySame
	default:
		return StrategyAbort
	}
}
