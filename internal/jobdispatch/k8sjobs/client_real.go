package k8sjobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// RealK8sClient wraps a kubernetes.Clientset to satisfy K8sClient.
type RealK8sClient struct {
	cs *kubernetes.Clientset
}

// NewInClusterClient builds a RealK8sClient from the in-cluster service account config.
func NewInClusterClient() (*RealK8sClient, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8sjobs: in-cluster config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sjobs: kubernetes client: %w", err)
	}
	return &RealK8sClient{cs: cs}, nil
}

// Compile-time interface check.
var _ K8sClient = (*RealK8sClient)(nil)

// CreateJob creates a batch/v1 Job with TTLSecondsAfterFinished=300 for auto-cleanup.
func (c *RealK8sClient) CreateJob(ctx context.Context, spec JobSpec) error {
	ttl := int32(300)
	backoff := int32(0)
	completions := int32(1)

	envVars := make([]corev1.EnvVar, 0, len(spec.Env))
	for _, kv := range spec.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("k8sjobs: malformed env entry %q (missing '=')", kv)
		}
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	name := spec.Name
	if name == "" {
		name = fmt.Sprintf("cf-runner-%d", time.Now().UnixMilli())
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spec.Namespace,
		},
		Spec: batchv1.JobSpec{
			Completions:             &completions,
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: spec.ServiceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: spec.Image,
							Args:  spec.Args,
							Env:   envVars,
						},
					},
				},
			},
		},
	}
	_, err := c.cs.BatchV1().Jobs(spec.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("k8sjobs: create job %q: %w", name, err)
	}
	return nil
}
