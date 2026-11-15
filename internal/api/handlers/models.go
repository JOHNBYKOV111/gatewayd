package handlers

import (
	"encoding/json"
	"net/http"
)

func Models() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// П13 подключит реальный список из конфига.
		// Пока что — корректный OpenAI-формат с пустым списком.
		resp := struct {
			Object string `json:"object"`
			Data   []any  `json:"data"`
		}{
			Object: "list",
			Data:   []any{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}
