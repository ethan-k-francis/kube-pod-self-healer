// Package remediation provides an HTTP client for the Python remediation
// service. When the watcher detects a pod failure, it calls this client to
// trigger automated remediation.
//
// The client is intentionally simple: serialize the event to JSON, POST it
// to the remediation service, and parse the response. Retries and circuit
// breaking are left to the caller or a future iteration — for a POC, fail-fast
// with good logging is the right trade-off.
package remediation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ethan-k-francis/infra-autopilot/agent/internal/watcher"
)

// Result is the response from the remediation service after processing an event.
type Result struct {
	EventID   string `json:"event_id"`
	Action    string `json:"action"`
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// Client sends pod health events to the remediation service via HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a remediation client pointing at the given base URL.
// The HTTP client has a 30-second timeout — remediation actions (pod delete,
// scale) should complete well within that window. If they don't, something
// is seriously wrong and we want to fail rather than hang.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendEvent posts a pod health event to the remediation service's /remediate
// endpoint. It returns the remediation result or an error if the request fails.
//
// This method is safe to call from multiple goroutines — the http.Client
// handles connection pooling internally.
func (c *Client) SendEvent(ctx context.Context, event watcher.PodHealthEvent) (*Result, error) {
	// Serialize the event to JSON. The field names match what the Python
	// FastAPI server expects (snake_case, matching the Pydantic model).
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	// Build the HTTP request with the context for cancellation support.
	// If the agent is shutting down, the context gets cancelled and the
	// request is aborted cleanly.
	url := c.baseURL + "/remediate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	log.Printf("[remediation-client] sending event %s to %s", event.EventID, url)

	// Execute the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Read the response body (limit to 1MB to prevent memory issues)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Check for non-2xx status codes. The remediation service returns 200
	// for successful remediation and 422 for validation errors.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remediation service returned %d: %s", resp.StatusCode, string(body))
	}

	// Parse the remediation result
	var result Result
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	log.Printf("[remediation-client] result: action=%s success=%v msg=%s",
		result.Action, result.Success, result.Message)

	return &result, nil
}
