package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: healthcheck <http-url>")
		os.Exit(2)
	}
	probeURL, err := localProbeURL(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil) // #nosec G704 -- validated loopback container probe URL.
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed")
		os.Exit(1)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request) // #nosec G704 -- request target is validated loopback only.
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed")
		os.Exit(1)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck failed")
		os.Exit(1)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		os.Exit(1)
	}
}

func localProbeURL(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.EscapedPath() != "/health/ready" {
		return "", errors.New("healthcheck URL must be a loopback readiness URL")
	}
	host := parsed.Hostname()
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return "", errors.New("healthcheck URL must be a loopback readiness URL")
	}
	return parsed.String(), nil
}
