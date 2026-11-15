package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/provider"
)

func TestWriteProviderError_Mapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{"unauthorized", provider.ErrUnauthorized, http.StatusUnauthorized, "invalid_request_error"},
		{"rate limited", provider.ErrRateLimited, http.StatusTooManyRequests, "rate_limit_error"},
		{"unavailable", provider.ErrProviderUnavailable, http.StatusBadGateway, "server_error"},
		{"timeout", provider.ErrTimeout, http.StatusGatewayTimeout, "server_error"},
		{"invalid request", provider.ErrInvalidRequest, http.StatusBadRequest, "invalid_request_error"},
		{"context canceled", context.Canceled, http.StatusBadRequest, "invalid_request_error"},
		{"unknown", errors.New("something broke"), http.StatusInternalServerError, "server_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeProviderError(rr, tc.err)

			if rr.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tc.wantStatus)
			}

			var resp struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid JSON: %v, body=%s", err, rr.Body.String())
			}
			if resp.Error.Type != tc.wantType {
				t.Errorf("error.type = %q, want %q", resp.Error.Type, tc.wantType)
			}
			if resp.Error.Message == "" {
				t.Error("error.message is empty")
			}
		})
	}
}
