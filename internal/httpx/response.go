package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse 遵循技术方案附录 A 的 OpenAI 兼容错误格式。
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      string `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError 写出统一错误响应。code 见附录 A 的错误码表（如 insufficient_balance、
// invalid_api_key、rate_limit_exceeded 等），type 沿用 OpenAI 的分类习惯。
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	errType := "invalid_request_error"
	switch status {
	case http.StatusUnauthorized:
		errType = "authentication_error"
	case http.StatusForbidden:
		errType = "permission_error"
	case http.StatusPaymentRequired:
		errType = "insufficient_quota"
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		errType = "api_error"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: ErrorBody{
		Message:   message,
		Type:      errType,
		Code:      code,
		RequestID: RequestIDFromContext(r.Context()),
	}})
}

// WriteJSON 写出普通 JSON 响应。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
