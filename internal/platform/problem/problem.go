package problem

import (
	"encoding/json"
	"net/http"

	"github.com/ah2909/e-commerce/internal/platform/httpserver"
)

type Body struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"request_id"`
}

func Write(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: httpserver.RequestID(r.Context()),
	})
}
