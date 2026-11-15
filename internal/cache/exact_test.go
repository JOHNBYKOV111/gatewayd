package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
)

func TestCache_Miss(t *testing.T) {
	c := New(time.Minute, 100)
	key := Key{Model: "llama3.2:3b", PromptHash: "abc", ParamsHash: "p1"}

	_, ok := c.Get(key)
	if ok {
		t.Fatal("expected miss on empty cache")
	}
}

func TestCache_SetGet(t *testing.T) {
	c := New(time.Minute, 100)
	key := Key{Model: "llama3.2:3b", PromptHash: "abc", ParamsHash: "p1"}
	resp := &openai.ChatResponse{
		ID:     "chatcmpl-1",
		Object: "chat.completion",
		Model:  "llama3.2:3b",
		Choices: []openai.Choice{
			{Index: 0, Message: openai.Message{Role: "assistant", Content: "Hello"}, FinishReason: "stop"},
		},
	}

	c.Set(key, resp)
	got, ok := c.Get(key)
	if !ok {
		t.Fatal("expected hit")
	}
	if got.ID != resp.ID {
		t.Errorf("ID = %q, want %q", got.ID, resp.ID)
	}
	if got.Choices[0].Message.Content != "Hello" {
		t.Errorf("content = %q, want Hello", got.Choices[0].Message.Content)
	}
}

func TestCache_DifferentKeysMiss(t *testing.T) {
	c := New(time.Minute, 100)
	k1 := Key{Model: "model-a", PromptHash: "h1", ParamsHash: "p1"}
	k2 := Key{Model: "model-a", PromptHash: "h2", ParamsHash: "p1"}
	k3 := Key{Model: "model-b", PromptHash: "h1", ParamsHash: "p1"}
	k4 := Key{Model: "model-a", PromptHash: "h1", ParamsHash: "p2"}

	c.Set(k1, &openai.ChatResponse{ID: "1"})

	for _, k := range []Key{k2, k3, k4} {
		if _, ok := c.Get(k); ok {
			t.Errorf("expected miss for %+v", k)
		}
	}
}

func TestCache_TTLExpired(t *testing.T) {
	c := New(50*time.Millisecond, 100)
	key := Key{Model: "m", PromptHash: "h", ParamsHash: "p"}

	c.Set(key, &openai.ChatResponse{ID: "1"})
	time.Sleep(60 * time.Millisecond)

	_, ok := c.Get(key)
	if ok {
		t.Fatal("expected expired entry to be missed")
	}
}

func TestCache_LRUEviction(t *testing.T) {
	c := New(time.Minute, 2) // ёмкость 2
	k1 := Key{Model: "m", PromptHash: "h1", ParamsHash: "p"}
	k2 := Key{Model: "m", PromptHash: "h2", ParamsHash: "p"}
	k3 := Key{Model: "m", PromptHash: "h3", ParamsHash: "p"}

	c.Set(k1, &openai.ChatResponse{ID: "1"})
	c.Set(k2, &openai.ChatResponse{ID: "2"})
	// Обращаемся к k1 — он становится "свежим".
	c.Get(k1)
	// Добавляем k3 — должен вытеснить k2 (самый старый).
	c.Set(k3, &openai.ChatResponse{ID: "3"})

	if _, ok := c.Get(k2); ok {
		t.Error("expected k2 to be evicted (LRU)")
	}
	if _, ok := c.Get(k1); !ok {
		t.Error("expected k1 to survive")
	}
	if _, ok := c.Get(k3); !ok {
		t.Error("expected k3 to be present")
	}
}

func TestCache_Stats(t *testing.T) {
	c := New(time.Minute, 100)
	key := Key{Model: "m", PromptHash: "h", ParamsHash: "p"}

	c.Get(key) // miss
	c.Set(key, &openai.ChatResponse{ID: "1"})
	c.Get(key) // hit
	c.Get(key) // hit

	stats := c.Stats()
	if stats.Hits != 2 {
		t.Errorf("Hits = %d, want 2", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
}

func TestCache_StreamChunks(t *testing.T) {
	c := New(time.Minute, 100)
	key := Key{Model: "m", PromptHash: "h", ParamsHash: "p"}

	chunks := []string{
		`{"choices":[{"delta":{"content":"A"}}]}`,
		`{"choices":[{"delta":{"content":"B"}}]}`,
		`[DONE]`,
	}
	c.SetStream(key, chunks)

	got, ok := c.GetStream(key)
	if !ok {
		t.Fatal("expected stream hit")
	}
	if len(got) != len(chunks) {
		t.Fatalf("chunks = %d, want %d", len(got), len(chunks))
	}
	for i, ch := range chunks {
		if got[i] != ch {
			t.Errorf("chunk[%d] = %q, want %q", i, got[i], ch)
		}
	}
}

func TestCache_Concurrent(t *testing.T) {
	// 10 горутин одновременно Set/Get. Проверяет sync.Mutex.
	// Запускать с -race: go test -race ./internal/cache/...
	c := New(time.Minute, 100)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := Key{
					Model:      "m",
					PromptHash: fmt.Sprintf("h-%d-%d", idx, j),
					ParamsHash: "p",
				}
				c.Set(key, &openai.ChatResponse{ID: fmt.Sprintf("resp-%d-%d", idx, j)})
				c.Get(key)
				c.GetStream(key) // miss, но проверяет mutex
			}
		}(i)
	}
	wg.Wait()

	// После 10*100 = 1000 Set/Get кэш не должен паниковать и не должен терять данные
	// сверх capacity (100). Точное число hits/misses не проверяем — оно зависит от
	// планировщика, но Stats() не должен паниковать.
	stats := c.Stats()
	if stats.Hits < 0 || stats.Misses < 0 {
		t.Errorf("negative stats: %+v", stats)
	}
}

func TestCache_HitRatio(t *testing.T) {
	c := New(time.Minute, 100)
	key := Key{Model: "m", PromptHash: "h", ParamsHash: "p"}

	// Пустой кэш: 0 hits, 0 misses → ratio 0.
	if got := c.HitRatio(); got != 0 {
		t.Errorf("empty cache HitRatio = %v, want 0", got)
	}

	// 1 miss.
	c.Get(key)
	if got := c.HitRatio(); got != 0 {
		t.Errorf("after 1 miss HitRatio = %v, want 0", got)
	}

	// 1 miss + 1 hit → 0.5.
	c.Set(key, &openai.ChatResponse{ID: "1"})
	c.Get(key)
	if got := c.HitRatio(); got != 0.5 {
		t.Errorf("after 1 miss + 1 hit HitRatio = %v, want 0.5", got)
	}

	// 1 miss + 2 hits → 2/3.
	c.Get(key)
	got := c.HitRatio()
	if got < 0.666 || got > 0.667 {
		t.Errorf("after 1 miss + 2 hits HitRatio = %v, want ~0.667", got)
	}
}
