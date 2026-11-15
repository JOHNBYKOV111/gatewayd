package openai

import (
	"testing"
)

func TestParseChatRequest_Valid(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-4o",
		"messages": [{"role": "user", "content": "hello"}],
		"stream": false
	}`)
	req, err := ParseChatRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %s", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Errorf("invalid messages parsed: %+v", req.Messages)
	}
}

func TestParseChatRequest_DuplicateKeys(t *testing.T) {
	// v1-семантика: дубликаты ключей разрешены (побеждает последний)
	raw := []byte(`{"model": "gpt-4o", "model": "gpt-3.5", "messages": [{"role": "user", "content": "hi"}]}`)
	req, err := ParseChatRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error on duplicate keys: %v", err)
	}
	if req.Model != "gpt-3.5" {
		t.Errorf("expected gpt-3.5 (last key wins), got %s", req.Model)
	}
}

func TestParseChatRequest_InvalidJSON(t *testing.T) {
	raw := []byte(`{"model": "gpt-4o", "messages": [}`)
	_, err := ParseChatRequest(raw)
	if err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestParseChatRequest_InvalidUTF8(t *testing.T) {
	// v1-семантика: невалидный UTF-8 не роняет парсер
	raw := []byte(`{"model": "gpt-4o", "messages": [{"role": "user", "content": "` + "\xff" + `"}]}`)
	_, err := ParseChatRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error on invalid UTF-8: %v", err)
	}
}

func TestErrorResponse_Format(t *testing.T) {
	errResp := NewErrorResponse("invalid_request_error", "test message")
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("wrong type: %s", errResp.Error.Type)
	}
	if errResp.Error.Message != "test message" {
		t.Errorf("wrong message: %s", errResp.Error.Message)
	}
}
