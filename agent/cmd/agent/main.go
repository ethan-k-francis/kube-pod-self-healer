// Package main is the entry point for the Kube Pod Self-Healer health agent.
//
// This is the fully integrated version that connects:
//   - Pod watcher (detects failures via K8s informers)
//   - Remediation client (sends events to the Python remediation service)
//   - Webhook notifier (sends alerts to Slack/Discord/custom endpoints)
//
// The agent supports two modes:
//   - In-cluster: when running as a pod, it uses the service account token
//   - Out-of-cluster: when running locally, it uses the KUBECONFIG env var
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ethan-k-francis/kube-pod-self-healer/agent/internal/config"
	"github.com/ethan-k-francis/kube-pod-self-healer/agent/internal/remediation"
	"github.com/ethan-k-francis/kube-pod-self-healer/agent/internal/watcher"
	"github.com/ethan-k-francis/kube-pod-self-healer/agent/internal/webhook"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.Println("[main] starting kube-pod-self-healer health agent")

	// --- Load Configuration ---
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[main] failed to load config: %v", err)
	}
	log.Printf("[main] config: %s", cfg)

	// --- Build Kubernetes Client ---
	k8sClient, err := buildK8sClient(cfg.Kubeconfig)
	if err != nil {
		log.Fatalf("[main] failed to create kubernetes client: %v", err)
	}
	log.Println("[main] kubernetes client initialized")

	// --- Initialize Remediation Client ---
	// The remediation client sends detected failures to the Python service
	// for automated response. It runs as a separate K8s service in the
	// kube-pod-self-healer namespace.
	remClient := remediation.NewClient(cfg.RemediationURL)
	log.Printf("[main] remediation client: url=%s", cfg.RemediationURL)

	// --- Initialize Webhook Notifier ---
	// The webhook notifier sends alerts to external systems. If no URL is
	// configured, notifications are silently skipped.
	notifier := webhook.NewNotifier(cfg.WebhookURL)
	if notifier.Enabled() {
		log.Println("[main] webhook notifications enabled")
	} else {
		log.Println("[main] webhook notifications disabled (no WEBHOOK_URL)")
	}

	// --- Create Watcher ---
	w := watcher.New(k8sClient, cfg.Namespace, cfg.CheckInterval)

	// --- Graceful Shutdown ---
	// Set up context cancellation before registering handlers, so the
	// context is available for handler goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register the event handler that ties everything together.
	// When a failure is detected:
	// 1. Log it
	// 2. Notify via webhook (detection alert)
	// 3. Send to remediation service
	// 4. Notify via webhook (remediation result)
	w.OnEvent(func(event watcher.PodHealthEvent) {
		log.Printf("[event] %s | pod=%s/%s container=%s restarts=%d | %s",
			event.FailureType,
			event.Namespace,
			event.PodName,
			event.ContainerName,
			event.RestartCount,
			event.Message,
		)

		// Send detection notification (async, fire-and-forget)
		go notifier.NotifyDetection(ctx, event)

		// Send to remediation service and wait for the result.
		// This is synchronous because we want the remediation result
		// before sending the follow-up webhook notification.
		result, err := remClient.SendEvent(ctx, event)
		if err != nil {
			log.Printf("[event] remediation failed: %v", err)
			go notifier.NotifyRemediation(ctx, event, "error", false, err.Error())
			return
		}

		// Send remediation result notification
		go notifier.NotifyRemediation(ctx, event, result.Action, result.Success, result.Message)
	})

	// Listen for shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigCh
		log.Printf("[main] received signal %s, shutting down gracefully", sig)
		cancel()
	}()

	// --- Start Watching ---
	log.Println("[main] starting pod health watcher")
	if err := w.Run(ctx); err != nil {
		log.Fatalf("[main] watcher error: %v", err)
	}

	log.Println("[main] shutdown complete")
}

// buildK8sClient creates a Kubernetes clientset. It tries in-cluster config
// first (when running as a pod), then falls back to the kubeconfig file.
func buildK8sClient(kubeconfigPath string) (kubernetes.Interface, error) {
	var k8sConfig *rest.Config
	var err error

	if kubeconfigPath != "" {
		log.Printf("[main] using kubeconfig: %s", kubeconfigPath)
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		log.Println("[main] using in-cluster config")
		k8sConfig, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, fmt.Errorf("build k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	return clientset, nil
}
