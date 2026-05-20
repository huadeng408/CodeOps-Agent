package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

type InputBuffer struct {
	reader *bufio.Reader
	writer io.Writer
	prompt string
}

func NewInputBuffer(in io.Reader, out io.Writer) *InputBuffer {
	return &InputBuffer{
		reader: bufio.NewReader(in),
		writer: out,
		prompt: "> ",
	}
}

func (b *InputBuffer) ReadLine(ctx context.Context) (string, error) {
	fmt.Fprint(b.writer, b.prompt)
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := b.reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			ch <- result{err: err}
			return
		}
		ch <- result{line: strings.TrimRight(line, "\r\n"), err: err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if errors.Is(res.err, io.EOF) && res.line == "" {
			return "", io.EOF
		}
		return res.line, res.err
	}
}
