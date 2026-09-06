package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

type Dependency struct {
	Name  string
	Check func(context.Context) error
}

type Handler struct {
	dependencies []Dependency
	timeout      time.Duration
}

type response struct {
	Status string        `json:"status"`
	Checks []checkResult `json:"checks,omitempty"`
}

type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type indexedResult struct {
	index int
	checkResult
}

func New(dependencies []Dependency, timeout time.Duration) *Handler {
	cloned := append([]Dependency(nil), dependencies...)
	sort.Slice(cloned, func(i, j int) bool { return cloned[i].Name < cloned[j].Name })
	return &Handler{dependencies: cloned, timeout: timeout}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/health/live", h.live)
	mux.HandleFunc("/health/ready", h.ready)
}

func (h *Handler) live(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	results := make([]checkResult, len(h.dependencies))
	resultCh := make(chan indexedResult, len(h.dependencies))
	for index, dependency := range h.dependencies {
		results[index] = checkResult{Name: dependency.Name, Status: "unavailable"}
		go func(index int, dependency Dependency) {
			status := "ok"
			if dependency.Check(ctx) != nil {
				status = "unavailable"
			}
			resultCh <- indexedResult{index: index, checkResult: checkResult{Name: dependency.Name, Status: status}}
		}(index, dependency)
	}

	ready := true
	for range h.dependencies {
		select {
		case result := <-resultCh:
			results[result.index] = result.checkResult
			if result.Status != "ok" {
				ready = false
			}
		case <-ctx.Done():
			writeJSON(w, http.StatusServiceUnavailable, response{Status: "not_ready", Checks: results})
			return
		}
	}

	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, response{Status: "not_ready", Checks: results})
		return
	}
	writeJSON(w, http.StatusOK, response{Status: "ready", Checks: results})
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	writeJSON(w, http.StatusMethodNotAllowed, response{Status: "method_not_allowed"})
}

func writeJSON(w http.ResponseWriter, status int, body response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
