package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

type StreamRenderer struct {
	mu  sync.Mutex
	out io.Writer
}

func NewStreamRenderer(out io.Writer) *StreamRenderer {
	return &StreamRenderer{out: out}
}

func (r *StreamRenderer) PrintLine(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintln(r.out, text)
}

func (r *StreamRenderer) PrintBlock(title string, lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintln(r.out, title)
	for _, line := range lines {
		fmt.Fprintln(r.out, "  "+line)
	}
}

func (r *StreamRenderer) Separator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintln(r.out, strings.Repeat("-", 72))
}
