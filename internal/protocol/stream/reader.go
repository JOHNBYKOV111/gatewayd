package stream

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
)

// Event — одно SSE-событие. Data — содержимое всех data:-строк события,
// склеенных через "\n" (как того требует спецификация SSE).
type Event struct {
	Data string
}

// Stream — канонический интерфейс потока.
type Stream interface {
	// Next возвращает следующее событие или io.EOF, когда поток закончился.
	Next() (Event, error)
	// Close закрывает лежащий в основе поток (если он реализует io.Closer).
	Close() error
}

type reader struct {
	ctx    context.Context
	src    *bufio.Reader
	done   bool
	closer io.Closer
}

// NewReader создаёт ридер без контекста.
func NewReader(src io.Reader) Stream {
	return NewReaderWithContext(context.Background(), src)
}

// NewReaderWithContext создаёт ридер, который уважает отмену контекста
// во время блокирующего чтения и закрывает источник (если он io.Closer)
// при вызове Close().
func NewReaderWithContext(ctx context.Context, src io.Reader) Stream {
	r := &reader{
		ctx: ctx,
		src: bufio.NewReader(src),
	}
	if c, ok := src.(io.Closer); ok {
		r.closer = c
	}
	return r
}

func (r *reader) Next() (Event, error) {
	if r.done {
		return Event{}, io.EOF
	}

	var dataLines []string

	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(dataLines) > 0 {
					return makeEvent(dataLines), nil
				}
				return Event{}, io.EOF
			}
			return Event{}, err
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			if len(dataLines) > 0 {
				return makeEvent(dataLines), nil
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")

		if field == "data" {
			if value == "[DONE]" {
				r.done = true
				if len(dataLines) > 0 {
					return makeEvent(dataLines), nil
				}
				return Event{}, io.EOF
			}
			dataLines = append(dataLines, value)
		}
	}
}

// Close закрывает исходный поток, если он реализует io.Closer.
// Повторный вызов безопасен (возвращает nil).
func (r *reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// readLine запускает блокирующий ReadString в горутине и ждёт либо строку,
// либо отмену контекста. Это единственный способ уважать ctx при io.Reader
// без дедлайна.
func (r *reader) readLine() (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		line, err := r.src.ReadString('\n')
		ch <- result{line: line, err: err}
	}()

	select {
	case <-r.ctx.Done():
		return "", r.ctx.Err()
	case res := <-ch:
		return res.line, res.err
	}
}

func makeEvent(lines []string) Event {
	return Event{Data: strings.Join(lines, "\n")}
}
