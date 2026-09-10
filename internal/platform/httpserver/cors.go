package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	corsAllowMethods = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"
	corsAllowHeaders = "Authorization, Content-Type, X-Request-ID"
)

func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return func(ctx *gin.Context) {
		origin := ctx.GetHeader("Origin")
		if _, ok := allowed[origin]; !ok {
			ctx.Next()
			return
		}
		ctx.Header("Access-Control-Allow-Origin", origin)
		ctx.Header("Access-Control-Allow-Methods", corsAllowMethods)
		ctx.Header("Access-Control-Allow-Headers", corsAllowHeaders)
		ctx.Header("Access-Control-Expose-Headers", requestIDHeader)
		ctx.Header("Access-Control-Max-Age", "600")
		ctx.Header("Vary", "Origin")
		if ctx.Request.Method == http.MethodOptions && ctx.GetHeader("Access-Control-Request-Method") != "" {
			ctx.AbortWithStatus(http.StatusNoContent)
			return
		}
		ctx.Next()
	}
}
