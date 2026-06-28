// Package watcher monitors Kubernetes pod health using informers.
//
// Informers are the idiomatic way to watch Kubernetes resources. Instead of
// polling the API server repeatedly (expensive and slow), an informer sets up
// a long-lived watch connection and maintains a local cache. When a pod
// changes, the informer calls our event handlers with the old and new state.
//
// This is dramatically more efficient than polling: one watch connection
// replaces thousands of list calls, and the local cache means reads don't
// hit the API server at all.
package watcher

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// EventHandler is a callback that consumers register to receive health events.
// The watcher doesn't know or care what happens with the event — it just
// calls all registered handlers. This decouples detection from remediation.
type EventHandler func(event PodHealthEvent)

// Watcher monitors pod health using Kubernetes informers and dispatches
// events to registered handlers when failures are detected.
type Watcher struct {
	clientset     kubernetes.Interface
	namespace     string
	checkInterval time.Duration
	handlers      []EventHandler
	// mu protects recentEvents and cooldown — informer callbacks run on
	// separate goroutines, so concurrent access is a data race without this.
	mu sync.Mutex
	// Track recently emitted events to avoid duplicate alerts for the same
	// ongoing failure. Key is "namespace/pod/container/failureType".
	recentEvents map[string]time.Time
	// Cooldown period before re-alerting on the same failure
	cooldown time.Duration
}

// New creates a Watcher that monitors pods in the given namespace.
// Pass empty string for namespace to watch all namespaces.
func New(clientset kubernetes.Interface, namespace string, checkInterval time.Duration) *Watcher {
	return &Watcher{
		clientset:     clientset,
		namespace:     namespace,
		checkInterval: checkInterval,
		handlers:      make([]EventHandler, 0),
		recentEvents:  make(map[string]time.Time),
		cooldown:      2 * time.Minute,
	}
}

// OnEvent registers a handler that will be called for every detected failure.
// Multiple handlers can be registered — they're called sequentially.
func (w *Watcher) OnEvent(handler EventHandler) {
	w.handlers = append(w.handlers, handler)
}

// Run starts the informer and blocks until the context is cancelled.
// This is the main loop of the watcher — it sets up the informer factory,
// registers event handlers, and then waits for the cache to sync before
// processing events.
func (w *Watcher) Run(ctx context.Context) error {
	log.Printf("[watcher] starting pod watcher namespace=%q interval=%s", w.namespace, w.checkInterval)

	// Create an informer factory. The resync period controls how often the
	// informer re-lists all objects to catch any missed events. This is our
	// "belt and suspenders" — the watch should catch everything, but the
	// periodic resync ensures we don't miss anything.
	var factory informers.SharedInformerFactory
	if w.namespace != "" {
		factory = informers.NewSharedInformerFactoryWithOptions(
			w.clientset,
			w.checkInterval,
			informers.WithNamespace(w.namespace),
		)
	} else {
		factory = informers.NewSharedInformerFactory(w.clientset, w.checkInterval)
	}

	// Get the pod informer. The factory manages its lifecycle.
	podInformer := factory.Core().V1().Pods().Informer()

	// Register event handlers. These callbacks fire when the informer
	// detects a change in the pod cache.
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		// UpdateFunc fires when an existing pod's status changes.
		// This is where we detect transitions into failure states.
		UpdateFunc: func(oldObj, newObj interface{}) {
			pod, ok := newObj.(*corev1.Pod)
			if !ok {
				return
			}
			w.checkPodHealth(pod)
		},
		// AddFunc fires when a new pod appears. We check it immediately
		// in case it was created in a failed state (e.g., bad image).
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			w.checkPodHealth(pod)
		},
	})

	// Start the factory, which starts all registered informers.
	factory.Start(ctx.Done())

	// Wait for the informer cache to sync (initial list from API server).
	// We don't want to process events until we have a complete picture
	// of the current cluster state.
	log.Println("[watcher] waiting for informer cache sync...")
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}
	log.Println("[watcher] cache synced, watching for failures")

	// Periodically clean up stale deduplication entries to prevent unbounded
	// memory growth. Runs every 5 minutes in a background goroutine.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.cleanupStaleEvents()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Block until the context is cancelled (graceful shutdown)
	<-ctx.Done()
	log.Println("[watcher] shutting down")
	return nil
}

// checkPodHealth inspects a pod's status and emits events for any detected
// failures. A single pod can have multiple containers, each of which can
// fail independently, so we check every container status.
func (w *Watcher) checkPodHealth(pod *corev1.Pod) {
	// Check all container statuses — both init containers and regular containers
	// can have failures we care about.
	for _, cs := range pod.Status.ContainerStatuses {
		w.checkContainerStatus(pod, cs)
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		w.checkContainerStatus(pod, cs)
	}
}

// checkContainerStatus examines a single container's status within a pod
// and emits the appropriate event if a failure is detected.
func (w *Watcher) checkContainerStatus(pod *corev1.Pod, cs corev1.ContainerStatus) {
	// --- CrashLoopBackOff Detection ---
	// A container in CrashLoopBackOff has a Waiting state with that specific
	// reason. This means the container keeps crashing and Kubernetes is
	// exponentially backing off on restart attempts.
	if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
		w.emitEvent(pod, cs, FailureCrashLoopBackOff,
			fmt.Sprintf("container %q in CrashLoopBackOff (restarts: %d)", cs.Name, cs.RestartCount))
		return
	}

	// --- ImagePullBackOff Detection ---
	// Similar to CrashLoopBackOff but for image pull failures. The image
	// doesn't exist, the tag is wrong, or registry credentials are missing.
	if cs.State.Waiting != nil {
		reason := cs.State.Waiting.Reason
		if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
			w.emitEvent(pod, cs, FailureImagePullBackOff,
				fmt.Sprintf("container %q cannot pull image: %s", cs.Name, cs.State.Waiting.Message))
			return
		}
	}

	// --- OOMKilled Detection ---
	// Check the last termination state. If the container was OOMKilled, the
	// reason is "OOMKilled" and the exit code is 137 (128 + SIGKILL=9).
	if cs.LastTerminationState.Terminated != nil &&
		cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
		w.emitEvent(pod, cs, FailureOOMKilled,
			fmt.Sprintf("container %q was OOMKilled (restarts: %d)", cs.Name, cs.RestartCount))
		return
	}

	// --- Probe Failure Detection ---
	// Probe failures are harder to detect from container status alone. We infer
	// them from the pod conditions: if the pod is not Ready and the container
	// is running (not crashed), it's likely a probe failure.
	if cs.State.Running != nil && !cs.Ready && cs.RestartCount > 0 {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionFalse {
				w.emitEvent(pod, cs, FailureProbeFailure,
					fmt.Sprintf("container %q running but not ready (possible probe failure, restarts: %d)",
						cs.Name, cs.RestartCount))
				return
			}
		}
	}
}

// emitEvent constructs a PodHealthEvent and dispatches it to all registered
// handlers. It deduplicates events using a cooldown period to avoid flooding
// downstream systems with repeated alerts for the same ongoing failure.
func (w *Watcher) emitEvent(pod *corev1.Pod, cs corev1.ContainerStatus, failureType FailureType, message string) {
	// Build a deduplication key from the failure's identity
	dedupeKey := fmt.Sprintf("%s/%s/%s/%s", pod.Namespace, pod.Name, cs.Name, failureType)

	// Lock protects recentEvents — informer callbacks run concurrently
	w.mu.Lock()
	// Check if we've recently emitted an event for this exact failure.
	// Without deduplication, the informer would fire an event on every
	// status update, potentially sending hundreds of alerts per minute.
	if lastEmit, exists := w.recentEvents[dedupeKey]; exists {
		if time.Since(lastEmit) < w.cooldown {
			w.mu.Unlock()
			return
		}
	}
	w.recentEvents[dedupeKey] = time.Now()
	w.mu.Unlock()

	// Resolve the pod's owner (Deployment, ReplicaSet, StatefulSet, etc.)
	// so remediation can act at the right level.
	ownerName, ownerKind := resolveOwner(pod)

	event := PodHealthEvent{
		EventID:       fmt.Sprintf("%s-%s-%d", pod.Name, cs.Name, time.Now().UnixMilli()),
		Timestamp:     time.Now().UTC(),
		FailureType:   failureType,
		PodName:       pod.Name,
		Namespace:     pod.Namespace,
		OwnerName:     ownerName,
		OwnerKind:     ownerKind,
		ContainerName: cs.Name,
		RestartCount:  cs.RestartCount,
		Message:       message,
		NodeName:      pod.Spec.NodeName,
	}

	log.Printf("[watcher] detected %s: %s", failureType, message)

	// Fan out to all registered handlers. Each handler runs synchronously
	// in this goroutine. If a handler needs to do async work (like HTTP calls),
	// it should spawn its own goroutine.
	for _, handler := range w.handlers {
		handler(event)
	}
}

// resolveOwner walks the pod's OwnerReferences to find the controlling
// resource. Pods created by Deployments have a chain: Deployment -> ReplicaSet -> Pod.
// We prefer the top-level owner (Deployment) for remediation purposes.
func resolveOwner(pod *corev1.Pod) (string, string) {
	if len(pod.OwnerReferences) == 0 {
		return "", ""
	}

	owner := pod.OwnerReferences[0]

	// ReplicaSet names include a hash suffix added by the Deployment controller
	// (e.g., "myapp-7f8b9c6d4f"). Strip the suffix to get the Deployment name.
	// This is a heuristic — it works for Deployment-managed ReplicaSets but not
	// for standalone ReplicaSets (which are rare in practice).
	if owner.Kind == "ReplicaSet" {
		parts := strings.Split(owner.Name, "-")
		if len(parts) > 2 {
			deployName := strings.Join(parts[:len(parts)-1], "-")
			return deployName, "Deployment"
		}
	}

	return owner.Name, owner.Kind
}

// cleanupStaleEvents removes deduplication entries older than twice the
// cooldown period. Called periodically to prevent unbounded memory growth
// in long-running agents.
func (w *Watcher) cleanupStaleEvents() {
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := time.Now().Add(-2 * w.cooldown)
	for key, ts := range w.recentEvents {
		if ts.Before(cutoff) {
			delete(w.recentEvents, key)
		}
	}
}

// PodCount returns the current number of tracked deduplication entries.
// Useful for health checks and debugging.
func (w *Watcher) PodCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.recentEvents)
}

// SetCooldown adjusts the deduplication cooldown period.
// Shorter cooldowns mean more alerts; longer means fewer but potentially
// delayed detection of recurring issues.
func (w *Watcher) SetCooldown(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cooldown = d
}

