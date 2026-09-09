package social

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ah2909/e-commerce/internal/identity/application"
	"golang.org/x/oauth2"
)

func TestClassifyProviderError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "rejected code", err: &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadRequest}}, want: application.ErrSocialProviderRejected},
		{name: "provider failure", err: &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadGateway}}, want: application.ErrSocialProviderUnavailable},
		{name: "provider rate limit", err: &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusTooManyRequests}}, want: application.ErrSocialProviderUnavailable},
		{name: "timeout", err: context.DeadlineExceeded, want: application.ErrSocialProviderUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyProviderError(test.err); !errors.Is(got, test.want) {
				t.Fatalf("classifyProviderError() = %v", got)
			}
		})
	}
}
