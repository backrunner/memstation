package observability

import (
	"context"
	"errors"
	"sort"
	"sync"
)

type Checker interface {
	Check(ctx context.Context) error
}

type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Registry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
}

func NewRegistry() *Registry {
	return &Registry{checkers: map[string]Checker{}}
}

func (r *Registry) Add(name string, checker Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[name] = checker
}

func (r *Registry) Run(ctx context.Context) ([]Component, error) {
	r.mu.RLock()
	names := make([]string, 0, len(r.checkers))
	for name := range r.checkers {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	components := make([]Component, 0, len(names))
	var errs []error
	for _, name := range names {
		r.mu.RLock()
		checker := r.checkers[name]
		r.mu.RUnlock()
		component := Component{Name: name, Status: "ok"}
		if checker != nil {
			if err := checker.Check(ctx); err != nil {
				component.Status = "error"
				component.Error = err.Error()
				errs = append(errs, err)
			}
		}
		components = append(components, component)
	}
	return components, errors.Join(errs...)
}

type CheckFunc func(ctx context.Context) error

func (f CheckFunc) Check(ctx context.Context) error { return f(ctx) }
