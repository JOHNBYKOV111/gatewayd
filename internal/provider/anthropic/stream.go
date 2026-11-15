package anthropic

import (
	"errors"
	"fmt"
	"io"
	"strings"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
)

// StreamConverter оборачивает сырой SSE-поток Anthropic
// и превращает его в канонический поток событий OpenAI.
type StreamConverter struct {
	raw stream.Stream

	// Состояние для кросс-фреймового merge usage.
	// output_tokens монотонно растёт, поэтому берём max.
	outputTokens int
	// input_tokens может прийти 0 в message_start, реальное значение позже,
	// поэтому берём первое непустое (не max).
	inputTokens int
	// finishReason копится из message_delta.
	finishReason string
	// done — флаг, что мы уже отдали финальный чанк.
	done bool
}

func NewStreamConverter(raw stream.Stream) *StreamConverter {
	return &StreamConverter{raw: raw}
}

func (c *StreamConverter) Next() (stream.Event, error) {
	if c.done {
		return stream.Event{}, io.EOF
	}

	for {
		raw, err := c.raw.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Если ничего не накопили (пустой поток) — отдаём io.EOF.
				// Если накопили — отдаём финальный чанк с finish_reason и usage.
				if c.outputTokens == 0 && c.inputTokens == 0 && c.finishReason == "" {
					return stream.Event{}, io.EOF
				}
				return c.flushFinal()
			}
			return stream.Event{}, err
		}

		// Разбираем событие Anthropic.
		data := raw.Data
		if !strings.HasPrefix(data, "{") {
			continue // служебные строки, не JSON
		}

		var ev anthEvent
		if err := jsonv2.Unmarshal([]byte(data), &ev); err != nil {
			// Битый чанк — пропускаем, чтобы не ломать поток.
			continue
		}

		switch ev.Type {
		case "message_start":
			c.mergeUsage(ev.Message.Usage)
		case "content_block_delta":
			if ev.Delta.Text != "" {
				return c.makeContentChunk(ev.Delta.Text)
			}
		case "message_delta":
			c.mergeUsage(ev.Usage)
			if ev.Delta.StopReason != "" {
				c.finishReason = mapStopReason(ev.Delta.StopReason)
			}
		case "message_stop":
			return c.flushFinal()
		}
		// ping, content_block_start, content_block_stop — игнорируем.
	}
}

func (c *StreamConverter) Close() error {
	return c.raw.Close()
}

// --- внутренние типы Anthropic SSE ---

type anthEvent struct {
	Type    string `json:"type"`
	Message struct {
		Usage anthUsage `json:"usage"`
	} `json:"message,omitempty"`
	Delta struct {
		Text       string `json:"text,omitempty"`
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage anthUsage `json:"usage,omitempty"`
}

type anthUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// mergeUsage применяет правила П12:
// - output_tokens: max-merge (монотонно растёт)
// - input_tokens: первое непустое (не max)
func (c *StreamConverter) mergeUsage(u anthUsage) {
	if u.OutputTokens > c.outputTokens {
		c.outputTokens = u.OutputTokens
	}
	if c.inputTokens == 0 && u.InputTokens > 0 {
		c.inputTokens = u.InputTokens
	}
}

// makeContentChunk формирует OpenAI-совместимый чанк с delta.content.
func (c *StreamConverter) makeContentChunk(text string) (stream.Event, error) {
	payload := fmt.Sprintf(
		`{"id":"chatcmpl-stream","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`,
		text,
	)
	return stream.Event{Data: payload}, nil
}

// flushFinal отдаёт финальный чанк с finish_reason и usage.
// Повторный вызов возвращает io.EOF (идемпотентность).
func (c *StreamConverter) flushFinal() (stream.Event, error) {
	if c.done {
		return stream.Event{}, io.EOF
	}
	c.done = true

	finish := c.finishReason
	if finish == "" {
		finish = "stop"
	}

	payload := fmt.Sprintf(
		`{"id":"chatcmpl-stream","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":%q}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		finish, c.inputTokens, c.outputTokens, c.inputTokens+c.outputTokens,
	)
	return stream.Event{Data: payload}, nil
}

func mapStopReason(anthReason string) string {
	switch anthReason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}
