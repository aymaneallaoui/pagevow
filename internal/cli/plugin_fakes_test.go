package cli_test

import (
	"context"
	"strings"
	"sync"
)

type commandCall struct {
	name string
	args []string
}

func (c commandCall) line() string {
	return strings.TrimSpace(c.name + " " + strings.Join(c.args, " "))
}

type fakeCommands struct {
	mu      sync.Mutex
	calls   []commandCall
	outputs map[string][]byte
	errs    map[string]error
	onRun   func(call commandCall)
}

func (f *fakeCommands) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	call := commandCall{name: name, args: args}
	f.calls = append(f.calls, call)
	onRun := f.onRun
	f.mu.Unlock()
	if onRun != nil {
		onRun(call)
	}
	key := strings.Join(args, " ")
	return f.outputs[key], f.errs[key]
}

func (f *fakeCommands) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	lines := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		lines = append(lines, call.line())
	}
	return lines
}

func (f *fakeCommands) fail(args string, err error) {
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[args] = err
}
