package shippinghttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ah2909/e-commerce/internal/shipping/domain"
	"github.com/gin-gonic/gin"
)

func TestRespondMapsDependencyTimeoutToServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/shipping/rule", nil)
	handler := Handler{logger: slog.New(slog.DiscardHandler)}

	handler.respond(ctx, http.StatusOK, domain.Rule{}, context.DeadlineExceeded)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("response status = %d", response.Code)
	}
}
