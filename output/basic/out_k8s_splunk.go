package main

import (
	"fmt"
	"log"
	"time"
	"unsafe"

	"github.com/fluent/fluent-bit-go/output"
)

import "C"

var (
	plugin *K8sSplunkPlugin
)

// K8sSplunkPlugin represents the Fluent Bit output plugin for Kubernetes to Splunk
type K8sSplunkPlugin struct {
	secretCache  *SecretCache
	splunkClient *SplunkHECClient
	k8sRetriever *K8sSecretRetriever
	config       *PluginConfig
}

// PluginConfig holds the configuration for the plugin
type PluginConfig struct {
	SplunkURL          string
	SplunkIndex        string
	SecretLabelKey     string
	SecretNamespace    string
	SecretKey          string
	InsecureSkipVerify bool
	CacheTTL           time.Duration
	MaxRetries         int
	RetryBackoff       time.Duration
}

// FLBPluginRegister is called by Fluent Bit to register the plugin
//
//export FLBPluginRegister
func FLBPluginRegister(def unsafe.Pointer) int {
	return output.FLBPluginRegister(def, "k8s_splunk", "Kubernetes to Splunk HEC output plugin")
}

// FLBPluginInit is called during plugin initialization
//
//export FLBPluginInit
func FLBPluginInit(ctx unsafe.Pointer) int {
	config := &PluginConfig{
		SplunkURL:       output.FLBPluginConfigKey(ctx, "splunk_url"),
		SplunkIndex:     output.FLBPluginConfigKey(ctx, "splunk_index"),
		SecretLabelKey:  output.FLBPluginConfigKey(ctx, "secret_label_key"),
		SecretNamespace: output.FLBPluginConfigKey(ctx, "secret_namespace"),
		SecretKey:       output.FLBPluginConfigKey(ctx, "secret_key"),
		CacheTTL:        time.Hour, // Default 1 hour
		MaxRetries:      3,
		RetryBackoff:    time.Second,
	}

	// Parse optional insecure_skip_verify setting
	if skipVerify := output.FLBPluginConfigKey(ctx, "insecure_skip_verify"); skipVerify == "true" || skipVerify == "on" {
		config.InsecureSkipVerify = true
	}

	// Validate required configuration
	if config.SplunkURL == "" {
		log.Printf("[k8s_splunk] ERROR: splunk_url is required")
		return output.FLB_ERROR
	}

	if config.SecretLabelKey == "" {
		config.SecretLabelKey = "splunk-token-secret" // Default label key
	}

	if config.SecretKey == "" {
		config.SecretKey = "token" // Default secret key
	}

	// Initialize components
	k8sRetriever, err := NewK8sSecretRetriever(config.SecretNamespace)
	if err != nil {
		log.Printf("[k8s_splunk] ERROR: Failed to initialize Kubernetes client: %v", err)
		return output.FLB_ERROR
	}

	splunkClient := NewSplunkHECClient(config.SplunkURL, config.SplunkIndex, config.InsecureSkipVerify)
	secretCache := NewSecretCache(config.CacheTTL)

	// Set the global plugin instance
	plugin = &K8sSplunkPlugin{
		secretCache:  secretCache,
		splunkClient: splunkClient,
		k8sRetriever: k8sRetriever,
		config:       config,
	}

	log.Printf("[k8s_splunk] Initialized successfully. Splunk URL: %s, Secret Label: %s", config.SplunkURL, config.SecretLabelKey)
	return output.FLB_OK
}

// FLBPluginFlushCtx is called when Fluent Bit needs to flush records
//
//export FLBPluginFlushCtx
func FLBPluginFlushCtx(ctx, data unsafe.Pointer, length C.int, tag *C.char) int {
	if plugin == nil {
		log.Printf("[k8s_splunk] ERROR: Plugin not initialized")
		return output.FLB_ERROR
	}

	// Decode Fluent Bit format
	dec := output.NewDecoder(data, int(length))

	// Track sent and failed records
	sentCount := 0
	failedCount := 0

	for {
		ret, ts, record := output.GetRecord(dec)
		if ret != 0 {
			break
		}

		// Process the record
		if err := plugin.processRecord(ts, record); err != nil {
			log.Printf("[k8s_splunk] ERROR: Failed to process record: %v", err)
			failedCount++
		} else {
			sentCount++
		}
	}

	log.Printf("[k8s_splunk] Processed %d records (sent: %d, failed: %d)", sentCount+failedCount, sentCount, failedCount)

	// Return FLB_OK if at least some records were sent, FLB_RETRY if all failed
	if sentCount > 0 {
		return output.FLB_OK
	}
	return output.FLB_RETRY
}

// processRecord processes a single log record
func (p *K8sSplunkPlugin) processRecord(timestamp interface{}, record map[interface{}]interface{}) error {
	// Extract Kubernetes metadata
	k8sMetadata, err := p.extractK8sMetadata(record)
	if err != nil {
		return fmt.Errorf("failed to extract Kubernetes metadata: %w", err)
	}

	// Get secret name from pod labels
	secretName, err := p.getSecretNameFromLabels(k8sMetadata)
	if err != nil {
		return fmt.Errorf("failed to get secret name from labels: %w", err)
	}

	// Get cached or fetch Splunk token
	token, err := p.getSplunkToken(secretName, k8sMetadata.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Splunk token: %w", err)
	}

	// Send to Splunk with retry logic
	return p.sendToSplunkWithRetry(token, timestamp, record, k8sMetadata)
}

// extractK8sMetadata extracts Kubernetes metadata from the record
func (p *K8sSplunkPlugin) extractK8sMetadata(record map[interface{}]interface{}) (*K8sMetadata, error) {
	k8sData, ok := record["kubernetes"].(map[interface{}]interface{})
	if !ok {
		return nil, fmt.Errorf("kubernetes metadata not found in record")
	}

	metadata := &K8sMetadata{}

	// Extract namespace
	if ns, ok := k8sData["namespace_name"].(string); ok {
		metadata.Namespace = ns
	} else if ns, ok := k8sData["namespace_name"].([]byte); ok {
		metadata.Namespace = string(ns)
	}

	// Extract pod name
	if pod, ok := k8sData["pod_name"].(string); ok {
		metadata.PodName = pod
	} else if pod, ok := k8sData["pod_name"].([]byte); ok {
		metadata.PodName = string(pod)
	}

	// Extract labels
	if labels, ok := k8sData["labels"].(map[interface{}]interface{}); ok {
		metadata.Labels = make(map[string]string)
		for k, v := range labels {
			if keyStr, ok := k.(string); ok {
				if valStr, ok := v.(string); ok {
					metadata.Labels[keyStr] = valStr
				} else if valBytes, ok := v.([]byte); ok {
					metadata.Labels[keyStr] = string(valBytes)
				}
			}
		}
	}

	if metadata.Namespace == "" || metadata.PodName == "" {
		return nil, fmt.Errorf("required Kubernetes metadata (namespace, pod_name) not found")
	}

	return metadata, nil
}

// getSecretNameFromLabels retrieves the secret name from pod labels
func (p *K8sSplunkPlugin) getSecretNameFromLabels(metadata *K8sMetadata) (string, error) {
	secretName, ok := metadata.Labels[p.config.SecretLabelKey]
	if !ok || secretName == "" {
		return "", fmt.Errorf("secret label '%s' not found in pod labels", p.config.SecretLabelKey)
	}
	return secretName, nil
}

// getSplunkToken retrieves the Splunk token from cache or Kubernetes secret
func (p *K8sSplunkPlugin) getSplunkToken(secretName, namespace string) (string, error) {
	cacheKey := fmt.Sprintf("%s/%s", namespace, secretName)

	// Check cache first
	if token, found := p.secretCache.Get(cacheKey); found {
		return token, nil
	}

	// If namespace is not in metadata, use configured namespace
	if namespace == "" {
		namespace = p.config.SecretNamespace
	}

	// Fetch from Kubernetes
	token, err := p.k8sRetriever.GetSecret(namespace, secretName, p.config.SecretKey)
	if err != nil {
		return "", fmt.Errorf("failed to fetch secret %s/%s: %w", namespace, secretName, err)
	}

	// Cache the token
	p.secretCache.Set(cacheKey, token)

	return token, nil
}

// sendToSplunkWithRetry sends the event to Splunk with exponential backoff retry
func (p *K8sSplunkPlugin) sendToSplunkWithRetry(token string, timestamp interface{}, record map[interface{}]interface{}, metadata *K8sMetadata) error {
	var lastErr error

	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := p.config.RetryBackoff * time.Duration(1<<uint(attempt-1))
			time.Sleep(backoff)
		}

		err := p.splunkClient.SendEvent(token, timestamp, record, metadata)
		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("[k8s_splunk] Attempt %d/%d failed: %v", attempt+1, p.config.MaxRetries+1, err)
	}

	return fmt.Errorf("failed after %d retries: %w", p.config.MaxRetries+1, lastErr)
}

// FLBPluginExit is called when the plugin is unloaded
//
//export FLBPluginExit
func FLBPluginExit() int {
	if plugin != nil && plugin.splunkClient != nil {
		plugin.splunkClient.Close()
	}
	log.Printf("[k8s_splunk] Plugin exiting")
	return output.FLB_OK
}

// K8sMetadata holds Kubernetes metadata extracted from records
type K8sMetadata struct {
	Namespace string
	PodName   string
	Labels    map[string]string
}

func main() {
	// This is required for plugins
}
