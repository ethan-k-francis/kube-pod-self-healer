// Package webhook sends notifications to external systems (Slack, Discord,
// PagerDuty, or any HTTP endpoint that accepts JSON POST requests).
//
// The notifier uses a generic JSON payload format that works with most webhook
// receivers. For Slack, the "text" field maps to the message body. For Discord,
// you'd wrap it in {"content": "..."}. The current format is a reasonable
// lowest-common-denominator that most systems can consume.
//
// Notifications are fire-and-forget: if the webhook fails, we log the error
// but don't retry or block the main event loop. Alert fatigue is a real risk,
// so we include enough context in each notification for the receiver to
// deduplicate and prioritize.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ethan-k-francis/kube-remediator/agent/internal/watcher"
)

// Payload is the JSON body sent to the webhook endpoint. It includes the
// detected failure details and, optionally, the remediation result.
type Payload struct {
	// Text is the main message — human-readable summary of what happened
	Text string `json:"text"`

	// Structured fields for programmatic consumption
	EventID     string `json:"event_id"`
	FailureType string `json:"failure_type"`
	PodName     string `json:"pod_name"`
	Namespace   string `json:"namespace"`
	Message     string `json:"message"`
	Timestamp   string `json:"timestamp"`

	// Remediation fields (populated after remediation completes)
	RemediationAction  string `json:"remediation_action,omitempty"`
	RemediationSuccess *bool  `json:"remediation_success,omitempty"`
	RemediationMessage string `json:"remediation_message,omitempty"`
}

// Notifier sends webhook notifications for pod health events.
type Notifier struct {
	webhookURL string
	httpClient *http.Client
}

// NewNotifier creates a webhook notifier. If webhookURL is empty, all
// notification calls become no-ops (logged but not sent).
func NewNotifier(webhookURL string) *Notifier {
	return &Notifier{
		webhookURL: webhookURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NotifyDetection sends a notification when a failure is first detected,
// before remediation has been attempted.
func (n *Notifier) NotifyDetection(ctx context.Context, event watcher.PodHealthEvent) {
	if n.webhookURL == "" {
		return
	}

	payload := Payload{
		Text: fmt.Sprintf(
			"[KubeRemediator] Detected %s: pod %s/%s (%s) — restarts: %d",
			event.FailureType,
			event.Namespace,
			event.PodName,
			event.ContainerName,
			event.RestartCount,
		),
		EventID:     event.EventID,
		FailureType: string(event.FailureType),
		PodName:     event.PodName,
		Namespace:   event.Namespace,
		Message:     event.Message,
		Timestamp:   event.Timestamp.Format(time.RFC3339),
	}

	n.send(ctx, payload)
}

// NotifyRemediation sends a notification after remediation has been attempted,
// including whether it succeeded or failed.
func (n *Notifier) NotifyRemediation(ctx context.Context, event watcher.PodHealthEvent, action string, success bool, message string) {
	if n.webhookURL == "" {
		return
	}

	statusIcon := "SUCCESS"
	if !success {
		statusIcon = "FAILED"
	}

	payload := Payload{
		Text: fmt.Sprintf(
			"[KubeRemediator] Remediation %s: %s on pod %s/%s — %s",
			statusIcon,
			action,
			event.Namespace,
			event.PodName,
			message,
		),
		EventID:            event.EventID,
		FailureType:        string(event.FailureType),
		PodName:            event.PodName,
		Namespace:          event.Namespace,
		Message:            event.Message,
		Timestamp:          time.Now().UTC().Format(time.RFC3339),
		RemediationAction:  action,
		RemediationSuccess: &success,
		RemediationMessage: message,
	}

	n.send(ctx, payload)
}

// Enabled returns whether webhook notifications are configured.
func (n *Notifier) Enabled() bool {
	return n.webhookURL != ""
}

// send serializes the payload and POSTs it to the webhook URL.
// Errors are logged but don't propagate — notifications should never
// block or crash the main agent loop.
func (n *Notifier) send(ctx context.Context, payload Payload) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[webhook] failed to marshal payload: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[webhook] failed to create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		log.Printf("[webhook] failed to send notification: %v", err)
		return
	}
	defer resp.Body.Close()
	// Drain the body so the underlying TCP connection can be reused by the pool.
	// Without this, HTTP/1.1 keep-alive connections are leaked.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		log.Printf("[webhook] notification returned status %d", resp.StatusCode)
		return
	}

	log.Printf("[webhook] notification sent: event=%s", payload.EventID)
}
