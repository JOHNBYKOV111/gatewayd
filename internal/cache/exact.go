package cache

import (
	"container/list"
	"sync"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
)

// Key — ключ exact-кэша: модель + хеш промпта + хеш параметров.
type Key struct {
	Model      string
	PromptHash string
	ParamsHash string
}

// Stats — счётчики попаданий и промахов.
type Stats struct {
	Hits   int64
	Misses int64
}

// entry — одна запись в кэше.
// resp — для нестримингового ответа, chunks — для стрима (nil, если это не стрим).
type entry struct {
	key      Key
	resp     *openai.ChatResponse
	chunks   []string
	expireAt time.Time
}

// Cache — exact-кэш с TTL и LRU-эвикцией.
// Потокобезопасен: Get/Set можно вызывать из разных горутин.
type Cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	items    map[Key]*list.Element
	order    *list.List // front = самый свежий, back = самый старый
	hits     int64
	misses   int64
}

// New создаёт кэш с TTL и максимальной ёмкостью.
// Если ttl <= 0 — записи не истекают. Если capacity <= 0 — кэш отключён (всегда miss).
func New(ttl time.Duration, capacity int) *Cache {
	return &Cache{
		ttl:      ttl,
		capacity: capacity,
		items:    make(map[Key]*list.Element),
		order:    list.New(),
	}
}

// Get возвращает нестриминговый ответ из кэша.
func (c *Cache) Get(key Key) (*openai.ChatResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.capacity <= 0 {
		c.misses++
		return nil, false
	}

	el, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}

	e := el.Value.(*entry)
	if c.expired(e) {
		c.remove(el)
		c.misses++
		return nil, false
	}

	// Если в записи только chunks (стрим), для Get это miss — нестриминговый ответ не кэширован.
	// НЕ инкрементим misses второй раз: это не «промах кэша», а «не тот тип записи».
	if e.resp == nil {
		return nil, false
	}

	c.order.MoveToFront(el)
	c.hits++
	return e.resp, true
}

// Set сохраняет нестриминговый ответ.
func (c *Cache) Set(key Key, resp *openai.ChatResponse) {
	if c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setLocked(key, &entry{key: key, resp: resp, expireAt: c.expireAt()})
}

// GetStream возвращает закэшированные стримовые чанки.
func (c *Cache) GetStream(key Key) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.capacity <= 0 {
		c.misses++
		return nil, false
	}

	el, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}

	e := el.Value.(*entry)
	if c.expired(e) {
		c.remove(el)
		c.misses++
		return nil, false
	}

	if e.chunks == nil {
		return nil, false
	}

	c.order.MoveToFront(el)
	c.hits++
	return e.chunks, true
}

// SetStream сохраняет стримовые чанки (полный текст ответа по чанкам).
func (c *Cache) SetStream(key Key, chunks []string) {
	if c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Копируем слайс, чтобы вызывающий не мог изменить кэш.
	cp := make([]string, len(chunks))
	copy(cp, chunks)

	c.setLocked(key, &entry{key: key, chunks: cp, expireAt: c.expireAt()})
}

// Stats возвращает счётчики hits/misses.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{Hits: c.hits, Misses: c.misses}
}

// HitRatio возвращает долю попаданий: Hits / (Hits + Misses).
// Если запросов не было (Hits + Misses == 0), возвращает 0.
func (c *Cache) HitRatio() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := c.hits + c.misses
	if total == 0 {
		return 0
	}
	return float64(c.hits) / float64(total)
}

// setLocked вставляет/обновляет запись и делает LRU-эвикцию.
// Вызывать только под mu.
func (c *Cache) setLocked(key Key, e *entry) {
	if el, ok := c.items[key]; ok {
		// Обновляем существующую запись.
		el.Value = e
		c.order.MoveToFront(el)
		return
	}

	// Новая запись.
	el := c.order.PushFront(e)
	c.items[key] = el

	// Эвикция: если превысили ёмкость, удаляем самый старый (back).
	for c.order.Len() > c.capacity {
		back := c.order.Back()
		if back == nil {
			break
		}
		c.remove(back)
	}
}

// remove удаляет элемент из map и списка. Вызывать только под mu.
func (c *Cache) remove(el *list.Element) {
	e := el.Value.(*entry)
	delete(c.items, e.key)
	c.order.Remove(el)
}

// expired проверяет, истёк ли TTL. Вызывать только под mu.
func (c *Cache) expired(e *entry) bool {
	if c.ttl <= 0 {
		return false
	}
	return time.Now().After(e.expireAt)
}

// expireAt возвращает время истечения для новой записи.
func (c *Cache) expireAt() time.Time {
	if c.ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(c.ttl)
}
