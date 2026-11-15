package repo

import (
	"context"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

func testCallLogPool(t *testing.T) *store.Store {
	t.Helper()
	dsn := testutil.StartPostgres(t)
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := store.Migrate(dsn, "../migrations"); err != nil {
		st.Close()
		t.Fatalf("store.Migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestCallLogRepo_Record_WithKey(t *testing.T) {
	st := testCallLogPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	callRepo := NewCallLogRepo(st.Pool)
	ctx := context.Background()

	keyID, err := keyRepo.Create(ctx, Key{
		KeyHash:       "hash-calllog-key",
		KeyPrefix:     "gw-",
		KeyLast4:      "test",
		Owner:         "owner-calllog",
		AllowedModels: []string{},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	log := CallLog{
		Timestamp:        now,
		KeyID:            &keyID,
		Model:            "llama3.2:3b",
		Provider:         "local-ollama",
		PromptHash:       "abc123hash",
		PromptTokens:     28,
		CompletionTokens: 12,
		TotalTokens:      40,
		CostRubles:       0.0,
		LatencyMS:        1500,
		TTFTMS:           200,
		Error:            "",
	}
	if err := callRepo.Record(ctx, log); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var (
		gotKeyID      *int64
		gotModel      string
		gotProvider   string
		gotPromptHash string
		gotPromptTok  int
		gotComplTok   int
		gotTotalTok   int
		gotError      string
		gotLatencyMS  int
		gotTTFTMS     int
	)
	row := st.Pool.QueryRow(ctx, `
		SELECT key_id, model, provider, prompt_hash, prompt_tokens, completion_tokens, total_tokens, error, latency_ms, ttft_ms
		FROM call_logs
		WHERE prompt_hash = $1
	`, "abc123hash")
	if err := row.Scan(&gotKeyID, &gotModel, &gotProvider, &gotPromptHash, &gotPromptTok, &gotComplTok, &gotTotalTok, &gotError, &gotLatencyMS, &gotTTFTMS); err != nil {
		t.Fatalf("scan call log: %v", err)
	}

	if gotKeyID == nil || *gotKeyID != keyID {
		t.Errorf("key_id = %v, want %d", gotKeyID, keyID)
	}
	if gotModel != "llama3.2:3b" {
		t.Errorf("model = %q", gotModel)
	}
	if gotProvider != "local-ollama" {
		t.Errorf("provider = %q", gotProvider)
	}
	if gotPromptHash != "abc123hash" {
		t.Errorf("prompt_hash = %q", gotPromptHash)
	}
	if gotPromptTok != 28 || gotComplTok != 12 || gotTotalTok != 40 {
		t.Errorf("tokens = %d/%d/%d, want 28/12/40", gotPromptTok, gotComplTok, gotTotalTok)
	}
	if gotLatencyMS != 1500 || gotTTFTMS != 200 {
		t.Errorf("latency/ttft = %d/%d, want 1500/200", gotLatencyMS, gotTTFTMS)
	}
	if gotError != "" {
		t.Errorf("error = %q, want empty", gotError)
	}
}

func TestCallLogRepo_Record_NilKey(t *testing.T) {
	st := testCallLogPool(t)
	callRepo := NewCallLogRepo(st.Pool)
	ctx := context.Background()

	log := CallLog{
		Timestamp:  time.Now().UTC(),
		KeyID:      nil,
		Model:      "llama3.2:3b",
		Provider:   "local-ollama",
		PromptHash: "hash-nil-key",
		Error:      "",
	}
	if err := callRepo.Record(ctx, log); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var keyID *int64
	row := st.Pool.QueryRow(ctx, `SELECT key_id FROM call_logs WHERE prompt_hash = $1`, "hash-nil-key")
	if err := row.Scan(&keyID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if keyID != nil {
		t.Errorf("key_id = %v, want nil", keyID)
	}
}

func TestCallLogRepo_Record_WithError(t *testing.T) {
	st := testCallLogPool(t)
	callRepo := NewCallLogRepo(st.Pool)
	ctx := context.Background()

	log := CallLog{
		Timestamp:  time.Now().UTC(),
		Model:      "llama3.2:3b",
		Provider:   "local-ollama",
		PromptHash: "hash-error",
		Error:      "provider unavailable",
	}
	if err := callRepo.Record(ctx, log); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var gotError string
	row := st.Pool.QueryRow(ctx, `SELECT error FROM call_logs WHERE prompt_hash = $1`, "hash-error")
	if err := row.Scan(&gotError); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotError != "provider unavailable" {
		t.Errorf("error = %q, want 'provider unavailable'", gotError)
	}
}

func TestCallLogRepo_Record_Multiple(t *testing.T) {
	st := testCallLogPool(t)
	callRepo := NewCallLogRepo(st.Pool)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		log := CallLog{
			Timestamp:  time.Now().UTC(),
			Model:      "llama3.2:3b",
			Provider:   "local-ollama",
			PromptHash: "hash-multiple",
		}
		if err := callRepo.Record(ctx, log); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	var count int
	row := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM call_logs WHERE prompt_hash = $1`, "hash-multiple")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

// TestCallLogRepo_UsageSummary — агрегация по ключам и моделям за период.
func TestCallLogRepo_UsageSummary(t *testing.T) {
	st := testCallLogPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	callRepo := NewCallLogRepo(st.Pool)
	ctx := context.Background()

	// Создаём два ключа.
	key1, err := keyRepo.Create(ctx, Key{
		KeyHash:       "hash-usage-1",
		KeyPrefix:     "gw-",
		KeyLast4:      "k1",
		Owner:         "owner-usage-1",
		AllowedModels: []string{},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key1: %v", err)
	}
	key2, err := keyRepo.Create(ctx, Key{
		KeyHash:       "hash-usage-2",
		KeyPrefix:     "gw-",
		KeyLast4:      "k2",
		Owner:         "owner-usage-2",
		AllowedModels: []string{},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key2: %v", err)
	}

	now := time.Now().UTC()

	// 2 записи для key1 (model-a), 1 для key1 (model-b), 1 для key2 (model-a).
	logs := []CallLog{
		{Timestamp: now, KeyID: &key1, Model: "model-a", Provider: "p1", PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CostRubles: 0.01, LatencyMS: 100, TTFTMS: 50},
		{Timestamp: now, KeyID: &key1, Model: "model-a", Provider: "p1", PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30, CostRubles: 0.02, LatencyMS: 200, TTFTMS: 60},
		{Timestamp: now, KeyID: &key1, Model: "model-b", Provider: "p2", PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8, CostRubles: 0.005, LatencyMS: 80, TTFTMS: 40},
		{Timestamp: now, KeyID: &key2, Model: "model-a", Provider: "p1", PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, CostRubles: 0.1, LatencyMS: 500, TTFTMS: 100},
	}
	for _, l := range logs {
		if err := callRepo.Record(ctx, l); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	// Агрегация за последние 7 дней (все записи попадут).
	summary, err := callRepo.UsageSummary(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}

	if summary.TotalRequests != 4 {
		t.Errorf("TotalRequests = %d, want 4", summary.TotalRequests)
	}
	if summary.TotalTokens != 15+30+8+150 {
		t.Errorf("TotalTokens = %d, want 203", summary.TotalTokens)
	}

	// Проверяем агрегацию по моделям.
	modelA := summary.ByModel["model-a"]
	if modelA.Requests != 3 {
		t.Errorf("model-a requests = %d, want 3", modelA.Requests)
	}
	if modelA.TotalTokens != 15+30+150 {
		t.Errorf("model-a tokens = %d, want 195", modelA.TotalTokens)
	}
	if modelA.CostRubles < 0.129 || modelA.CostRubles > 0.131 {
		t.Errorf("model-a cost = %v, want ~0.13", modelA.CostRubles)
	}

	// Проверяем агрегацию по ключам.
	key1Agg := summary.ByKey[key1]
	if key1Agg.Requests != 3 {
		t.Errorf("key1 requests = %d, want 3", key1Agg.Requests)
	}
	if key1Agg.TotalTokens != 15+30+8 {
		t.Errorf("key1 tokens = %d, want 53", key1Agg.TotalTokens)
	}
}
