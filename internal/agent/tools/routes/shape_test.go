package routes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	in := map[string]any{
		"name": "acme", "api_key": "sk-123", "secret_last4": "abcd", "tokens_in": 10,
		"email": "alice@example.com", "client_ip": "10.0.0.1", "description": "联系 13812345678 或 bob@x.io",
		"items": []any{map[string]any{"password": "p"}},
	}
	out := Redact(in).(map[string]any)
	b, _ := json.Marshal(out)
	s := string(b)
	for _, leaked := range []string{"sk-123", "alice@", "13812345678", `"p"`, "bob@x.io", "10.0.0.1"} {
		if strings.Contains(s, leaked) {
			t.Errorf("leaked %q in %s", leaked, s)
		}
	}
	if out["secret_last4"] != "abcd" || out["tokens_in"] != 10 || out["name"] != "acme" {
		t.Errorf("over-redacted: %s", s)
	}
}

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{"changeRequestID": "change_request_id", "runID": "run_id", "requestID": "request_id"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%s) = %s", in, got)
		}
	}
}
