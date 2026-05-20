package hooks

import (
	"context"
	"sync"
)

type Engine struct {
	mu       sync.RWMutex
	handlers map[Phase][]Handler
}

func NewEngine() *Engine {
	return &Engine{handlers: map[Phase][]Handler{}}
}

func (e *Engine) Register(phase Phase, handler Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.handlers[phase] = append(e.handlers[phase], handler)
}

func (e *Engine) Run(ctx context.Context, phase Phase, hook Context) ([]Result, error) {
	e.mu.RLock()
	handlers := append([]Handler(nil), e.handlers[phase]...)
	e.mu.RUnlock()

	results := make([]Result, 0, len(handlers))
	for _, handler := range handlers {
		result, err := handler(ctx, hook)
		if err != nil {
			return results, err
		}
		results = append(results, result)
		if result.Cancel {
			break
		}
	}
	return results, nil
}
