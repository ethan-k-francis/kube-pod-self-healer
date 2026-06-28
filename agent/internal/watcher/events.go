// Package watcher monitors Kubernetes pod health using informers.
// This file defines the event types that flow from the watcher to
// consumers (remediation client, webhook notifier, logging).
package watcher

import "time"

// FailureType categorizes the kind of pod failure detected.
// Using a typed string (not raw strings) gives us compile-time safety
// and makes it easy to add new failure types without breaking existing code.
type FailureType string

const (
	// FailureCrashLoopBackOff — container keeps crashing and Kubernetes is
	// backing off on restarts. The most common "something is wrong" signal.
	FailureCrashLoopBackOff FailureType = "CrashLoopBackOff"

	// FailureOOMKilled — container exceeded its memory limit and the kernel
	// OOM killer terminated it. Usually means the memory limit is too low
	// or the app has a memory leak.
	FailureOOMKilled FailureType = "OOMKilled"

	// FailureProbeFailure — liveness or readiness probe failed. The container
	// is running but not responding correctly. Could be a deadlock, resource
	// exhaustion, or dependency failure.
	FailureProbeFailure FailureType = "ProbeFailure"

	// FailureImagePullBackOff — Kubernetes can't pull the container image.
	// Wrong image name, missing tag, or registry auth issues.
	FailureImagePullBackOff FailureType = "ImagePullBackOff"
)

// PodHealthEvent represents a detected failure in a pod. This struct is
// serialized to JSON and sent to both the remediation service and webhook.
type PodHealthEvent struct {
	// Unique event ID for deduplication and tracking
	EventID string `json:"event_id"`

	// Timestamp when the failure was detected
	Timestamp time.Time `json:"timestamp"`

	// Which type of failure was detected
	FailureType FailureType `json:"failure_type"`

	// Pod identification — enough info to find and act on the pod
	PodName   string `json:"pod_name"`
	Namespace string `json:"namespace"`

	// The deployment (or other owner) that manages this pod.
	// Needed for scale-based remediation.
	OwnerName string `json:"owner_name"`
	OwnerKind string `json:"owner_kind"`

	// Container that failed (a pod can have multiple containers)
	ContainerName string `json:"container_name"`

	// How many times this container has restarted. High restart counts
	// indicate a persistent problem that simple restarts won't fix.
	RestartCount int32 `json:"restart_count"`

	// Human-readable description of what happened
	Message string `json:"message"`

	// The node the pod is running on — useful for node-level correlation
	NodeName string `json:"node_name"`
}
