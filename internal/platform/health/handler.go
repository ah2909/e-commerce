package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
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

func (h *Handler) Register(router gin.IRoutes) {
	for _, path := range []string{"/health/live", "/health/ready"} {
		for _, method := range []string{http.MethodConnect, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace} {
			router.Handle(method, path, h.methodNotAllowed)
		}
	}
	router.GET("/health/live", h.live)
	router.GET("/health/ready", h.ready)
}

func (h *Handler) live(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, response{Status: "ok"})
}

func (h *Handler) methodNotAllowed(ctx *gin.Context) {
	ctx.Header("Allow", http.MethodGet)
	ctx.Header("Content-Type", "application/json")
	ctx.Status(http.StatusMethodNotAllowed)
	_ = json.NewEncoder(ctx.Writer).Encode(response{Status: "method_not_allowed"})
}

func (h *Handler) ready(request *gin.Context) {
	ctx, cancel := context.WithTimeout(request.Request.Context(), h.timeout)
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
			request.JSON(http.StatusServiceUnavailable, response{Status: "not_ready", Checks: results})
			return
		}
	}

	if !ready {
		request.JSON(http.StatusServiceUnavailable, response{Status: "not_ready", Checks: results})
		return
	}
	request.JSON(http.StatusOK, response{Status: "ready", Checks: results})
}
