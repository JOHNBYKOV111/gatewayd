package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

func testAdminDeps(t *testing.T) (*repo.KeyRepo, *repo.CallLogRepo, *store.Store) {
	t.Helper()
	dsn := testutil.StartPostgres(t)
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := store.Migrate(dsn, "../../store/migrations"); err != nil {
		st.Close()
		t.Fatalf("store.Migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return repo.NewKeyRepo(st.Pool), repo.NewCallLogRepo(st.Pool), st
}

func TestAdminKeys_Usage(t *testing.T) {
	keyRepo, callRepo, _ := testAdminDeps(t)
	ctx := context.Background()

	keyID, err := keyRepo.Create(ctx, repo.Key{
		KeyHash:       "hash-admin-usage",
		KeyPrefix:     "gw-",
		KeyLast4:      "test",
		Owner:         "owner-admin",
		AllowedModels: []string{},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	now := time.Now().UTC()
	logs := []repo.CallLog{
		{Timestamp: now, KeyID: &keyID, Model: "model-a", Provider: "p1", PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CostRubles: 0.01},
		{Timestamp: now, KeyID: &keyID, Model: "model-a", Provider: "p1", PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30, CostRubles: 0.02},
		{Timestamp: now, KeyID: &keyID, Model: "model-b", Provider: "p2", PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8, CostRubles: 0.005},
	}
	for _, l := range logs {
		if err := callRepo.Record(ctx, l); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	admin := NewAdminKeys(keyRepo, callRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/usage?range=7d", nil)
	rr := httptest.NewRecorder()
	admin.Usage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp struct {
		Range         string  `json:"range"`
		TotalRequests int     `json:"total_requests"`
		TotalTokens   int     `json:"total_tokens"`
		TotalCost     float64 `json:"total_cost"`
		ByModel       map[string]struct {
			Requests    int     `json:"Requests"`
			TotalTokens int     `json:"TotalTokens"`
			CostRubles  float64 `json:"CostRubles"`
		} `json:"by_model"`
		ByKey map[string]struct {
			Requests    int     `json:"Requests"`
			TotalTokens int     `json:"TotalTokens"`
			CostRubles  float64 `json:"CostRubles"`
		} `json:"by_key"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v, body: %s", err, rr.Body.String())
	}

	if resp.Range != "7d" {
		t.Errorf("range = %q, want 7d", resp.Range)
	}
	if resp.TotalRequests != 3 {
		t.Errorf("total_requests = %d, want 3", resp.TotalRequests)
	}
	if resp.TotalTokens != 53 {
		t.Errorf("total_tokens = %d, want 53", resp.TotalTokens)
	}

	if len(resp.ByModel) != 2 {
		t.Errorf("by_model len = %d, want 2", len(resp.ByModel))
	}
	if resp.ByModel["model-a"].Requests != 2 {
		t.Errorf("model-a requests = %d, want 2", resp.ByModel["model-a"].Requests)
	}
	if resp.ByModel["model-a"].TotalTokens != 45 {
		t.Errorf("model-a tokens = %d, want 45", resp.ByModel["model-a"].TotalTokens)
	}

	if len(resp.ByKey) != 1 {
		t.Errorf("by_key len = %d, want 1", len(resp.ByKey))
	}
	var found bool
	for k, v := range resp.ByKey {
		if v.Requests != 3 {
			t.Errorf("by_key[%s] requests = %d, want 3", k, v.Requests)
		}
		if v.TotalTokens != 53 {
			t.Errorf("by_key[%s] tokens = %d, want 53", k, v.TotalTokens)
		}
		found = true
	}
	if !found {
		t.Error("by_key is empty")
	}
}

func TestAdminKeys_Usage_InvalidRange(t *testing.T) {
	keyRepo, callRepo, _ := testAdminDeps(t)
	admin := NewAdminKeys(keyRepo, callRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/usage?range=abc", nil)
	rr := httptest.NewRecorder()
	admin.Usage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid range") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

func TestAdminKeys_Usage_DefaultRange(t *testing.T) {
	keyRepo, callRepo, _ := testAdminDeps(t)
	admin := NewAdminKeys(keyRepo, callRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/usage", nil)
	rr := httptest.NewRecorder()
	admin.Usage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Range string `json:"range"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Range != "7d" {
		t.Errorf("default range = %q, want 7d", resp.Range)
	}
}
