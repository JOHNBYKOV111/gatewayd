package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/auth"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

type AdminKeys struct {
	keyRepo  *repo.KeyRepo
	callRepo *repo.CallLogRepo
}

func NewAdminKeys(keyRepo *repo.KeyRepo, callRepo *repo.CallLogRepo) *AdminKeys {
	return &AdminKeys{keyRepo: keyRepo, callRepo: callRepo}
}

func (a *AdminKeys) CreateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Owner         string   `json:"owner"`
		AllowedModels []string `json:"allowed_models"`
		RPMLimit      int      `json:"rpm_limit"`
		TPMLimit      int      `json:"tpm_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}
	if req.RPMLimit == 0 {
		req.RPMLimit = 60
	}
	if req.TPMLimit == 0 {
		req.TPMLimit = 100000
	}

	key, err := auth.GenerateKey()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "failed to generate key")
		return
	}
	hash := auth.HashKey(key)
	prefix, last4 := auth.PrefixAndLast4(key)

	id, err := a.keyRepo.Create(r.Context(), repo.Key{
		KeyHash:       hash,
		KeyPrefix:     prefix,
		KeyLast4:      last4,
		Owner:         req.Owner,
		AllowedModels: req.AllowedModels,
		RPMLimit:      req.RPMLimit,
		TPMLimit:      req.TPMLimit,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "failed to create key")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":    id,
		"key":   key,
		"owner": req.Owner,
	})
}

func (a *AdminKeys) ListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.keyRepo.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "failed to list keys")
		return
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{
			"id":         k.ID,
			"prefix":     k.KeyPrefix,
			"last4":      k.KeyLast4,
			"owner":      k.Owner,
			"created_at": k.CreatedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
}

// Usage — GET /admin/usage?range=7d.
// Отдаёт агрегаты из call_logs за период (по умолчанию 7d).
func (a *AdminKeys) Usage(w http.ResponseWriter, r *http.Request) {
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "7d"
	}

	dur, err := parseRange(rangeStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid range: "+err.Error())
		return
	}

	since := time.Now().Add(-dur)
	summary, err := a.callRepo.UsageSummary(r.Context(), since)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "failed to get usage")
		return
	}

	// Готовим JSON-ответ: map'ы с числовыми ключами → строки.
	byKey := make(map[string]repo.KeyUsage, len(summary.ByKey))
	for id, u := range summary.ByKey {
		byKey[strconv.FormatInt(id, 10)] = u
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"range":          rangeStr,
		"total_requests": summary.TotalRequests,
		"total_tokens":   summary.TotalTokens,
		"total_cost":     summary.TotalCost,
		"by_model":       summary.ByModel,
		"by_key":         byKey,
	})
}

// parseRange парсит строку вида "7d", "24h", "30m" в time.Duration.
func parseRange(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, strconv.ErrSyntax
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, strconv.ErrSyntax
	}
	switch strings.ToLower(s[len(s)-1:]) {
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "m":
		return time.Duration(n) * time.Minute, nil
	default:
		return 0, strconv.ErrSyntax
	}
}
