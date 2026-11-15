package routing

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// fakeProviderControls — управляемый фейк-провайдер для тестов П14–П18.
// Позволяет задать сценарий отказа (429, таймаут, обрыв стрима) и задержку.
type fakeProviderControls struct {
	name string

	// err — ошибка, которую вернёт Do/Stream (если не nil).
	err error
	// delay — задержка перед ответом (эмулирует медленный провайдер).
	delay time.Duration
	// streamChunks — сколько чанков отдать до обрыва (0 = все).
	// Если streamChunks > 0 и упадёт посреди — вернёт ошибку после N чанков.
	streamChunks int
	// resp — ответ, который вернётся при успехе.
	resp openai.ChatResponse
	// chunks — чанки для стрима.
	chunks []string

	// mu/счётчики для проверки вызовов (retry, breaker).
	doCalls     int
	streamCalls int
}

func (f *fakeProviderControls) Name() string { return f.name }

func (f *fakeProviderControls) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true, Streaming: true}
}

func (f *fakeProviderControls) Do(ctx context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	f.doCalls++

	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return openai.ChatResponse{}, provider.NormalizeError(ctx.Err())
		case <-time.After(f.delay):
		}
	}

	if f.err != nil {
		return openai.ChatResponse{}, f.err
	}
	return f.resp, nil
}

func (f *fakeProviderControls) Stream(ctx context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	f.streamCalls++

	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, provider.NormalizeError(ctx.Err())
		case <-time.After(f.delay):
		}
	}

	if f.err != nil {
		return nil, f.err
	}

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for i, c := range f.chunks {
			if f.streamChunks > 0 && i >= f.streamChunks {
				// Эмуляция обрыва посреди стрима.
				_ = pw.CloseWithError(fmt.Errorf("fake: stream aborted after %d chunks", f.streamChunks))
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			if _, err := pw.Write([]byte("data: " + c + "\n\n")); err != nil {
				return
			}
		}
	}()
	return stream.NewReaderWithContext(ctx, pr), nil
}

func (f *fakeProviderControls) HealthCheck(_ context.Context) error { return nil }

func (f *fakeProviderControls) Usage(resp openai.ChatResponse) openai.Usage {
	if resp.Usage == nil {
		return openai.Usage{}
	}
	return *resp.Usage
}

// DoCalls возвращает количество вызовов Do (для проверки retry/breaker).
func (f *fakeProviderControls) DoCalls() int { return f.doCalls }

// StreamCalls возвращает количество вызовов Stream.
func (f *fakeProviderControls) StreamCalls() int { return f.streamCalls }
