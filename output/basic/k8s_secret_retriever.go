package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sSecretRetriever handles fetching secrets from Kubernetes
type K8sSecretRetriever struct {
	clientset        *kubernetes.Clientset
	defaultNamespace string
}

// NewK8sSecretRetriever creates a new Kubernetes secret retriever
// It automatically detects whether it's running in-cluster or outside the cluster
func NewK8sSecretRetriever(defaultNamespace string) (*K8sSecretRetriever, error) {
	config, err := getKubernetesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// If no default namespace is provided, try to detect it
	if defaultNamespace == "" {
		defaultNamespace = detectNamespace()
	}

	return &K8sSecretRetriever{
		clientset:        clientset,
		defaultNamespace: defaultNamespace,
	}, nil
}

// getKubernetesConfig returns the Kubernetes client configuration
// It first tries in-cluster config, then falls back to kubeconfig file
func getKubernetesConfig() (*rest.Config, error) {
	// Try in-cluster config first (for when plugin runs in a pod)
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig file (for local development/testing)
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
	}

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	return config, nil
}

// detectNamespace tries to detect the current namespace
// Returns "default" if detection fails
func detectNamespace() string {
	// Try to read namespace from service account mount (in-cluster)
	namespaceBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil {
		return string(namespaceBytes)
	}

	// Fall back to default namespace
	return "default"
}

// GetSecret retrieves a specific key from a Kubernetes secret
func (k *K8sSecretRetriever) GetSecret(namespace, secretName, key string) (string, error) {
	// Use default namespace if not specified
	if namespace == "" {
		namespace = k.defaultNamespace
	}

	// Fetch the secret from Kubernetes
	secret, err := k.clientset.CoreV1().Secrets(namespace).Get(
		context.Background(),
		secretName,
		metav1.GetOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	// Extract the specified key from the secret data
	value, exists := secret.Data[key]
	if !exists {
		return "", fmt.Errorf("key '%s' not found in secret %s/%s", key, namespace, secretName)
	}

	return string(value), nil
}

// GetSecretObject retrieves the entire Kubernetes secret object
// This is useful if you need to access multiple keys or metadata
func (k *K8sSecretRetriever) GetSecretObject(namespace, secretName string) (*corev1.Secret, error) {
	if namespace == "" {
		namespace = k.defaultNamespace
	}

	secret, err := k.clientset.CoreV1().Secrets(namespace).Get(
		context.Background(),
		secretName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	return secret, nil
}

// SecretExists checks if a secret exists in the specified namespace
func (k *K8sSecretRetriever) SecretExists(namespace, secretName string) (bool, error) {
	if namespace == "" {
		namespace = k.defaultNamespace
	}

	_, err := k.clientset.CoreV1().Secrets(namespace).Get(
		context.Background(),
		secretName,
		metav1.GetOptions{},
	)
	if err != nil {
		// Check if it's a "not found" error
		if statusErr, ok := err.(interface{ Status() metav1.Status }); ok {
			if statusErr.Status().Code == 404 {
				return false, nil
			}
		}
		return false, err
	}

	return true, nil
}
