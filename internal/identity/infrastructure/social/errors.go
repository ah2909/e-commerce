package social

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/ah2909/e-commerce/internal/identity/application"
	"golang.org/x/oauth2"
)

func classifyProviderError(err error) error {
	if err == nil {
		return nil
	}
	var retrieveError *oauth2.RetrieveError
	if errors.As(err, &retrieveError) && retrieveError.Response != nil {
		if retrieveError.Response.StatusCode != http.StatusRequestTimeout && retrieveError.Response.StatusCode != http.StatusTooManyRequests && retrieveError.Response.StatusCode < http.StatusInternalServerError {
			return application.ErrSocialProviderRejected
		}
		return application.ErrSocialProviderUnavailable
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) {
		return application.ErrSocialProviderUnavailable
	}
	return application.ErrSocialProviderUnavailable
}

func classifyClaimsError(err error) error {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) {
		return application.ErrSocialProviderUnavailable
	}
	return application.ErrSocialProviderRejected
}
