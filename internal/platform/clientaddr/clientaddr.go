package clientaddr

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	maxForwardedBytes = 2048
	maxForwardedHops  = 16
)

type contextKey struct{}

type Resolver struct {
	trusted []netip.Prefix
}

func New(trusted []netip.Prefix) Resolver {
	return Resolver{trusted: append([]netip.Prefix(nil), trusted...)}
}

func (resolver Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address := resolver.resolve(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, address.String())))
	})
}

func (resolver Resolver) GinMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		address := resolver.resolve(ctx.Request)
		ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), contextKey{}, address.String()))
		ctx.Next()
	}
}

func FromRequest(r *http.Request) string {
	if value, ok := r.Context().Value(contextKey{}).(string); ok && value != "" {
		return value
	}
	address, ok := remoteAddress(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	return address.String()
}

func (resolver Resolver) resolve(r *http.Request) netip.Addr {
	peer, ok := remoteAddress(r.RemoteAddr)
	if !ok || !resolver.isTrusted(peer) {
		return peer
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) != 1 || len(values[0]) > maxForwardedBytes {
		return peer
	}
	parts := strings.Split(values[0], ",")
	if len(parts) == 0 || len(parts) > maxForwardedHops {
		return peer
	}
	chain := make([]netip.Addr, len(parts))
	for index, part := range parts {
		address, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return peer
		}
		chain[index] = address.Unmap()
	}
	current := peer
	for index := len(chain) - 1; index >= 0; index-- {
		if !resolver.isTrusted(current) {
			return current
		}
		current = chain[index]
	}
	return current
}

func (resolver Resolver) isTrusted(address netip.Addr) bool {
	for _, prefix := range resolver.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func remoteAddress(value string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		value = host
	}
	address, err := netip.ParseAddr(strings.Trim(value, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}
