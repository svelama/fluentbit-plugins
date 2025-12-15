package main

import (
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/fluent/fluent-bit-go/output"
)

import "C"

var (
	plugin *K8sSplunkPlugin
)

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

// K8sMetadata holds Kubernetes metadata extracted from records
type K8sMetadata struct {
	Namespace string
	PodName   string
	Labels    map[string]string
}

// K8sSplunkPlugin represents the Fluent Bit output plugin with worker pool and batching
type K8sSplunkPlugin struct {
	secretCache  *SecretCache
	splunkClient *SplunkHECClient
	k8sRetriever *K8sSecretRetriever
	config       *PluginConfig
	workerPool   *WorkerPool
	batcher      *LogBatcher
}

// WorkerPool manages concurrent log processing
type WorkerPool struct {
	workers  int
	jobs     chan *LogJob
	wg       sync.WaitGroup
	stopChan chan struct{}
	plugin   *K8sSplunkPlugin
}

// LogJob represents a single log to be processed
type LogJob struct {
	timestamp interface{}
	record    map[interface{}]interface{}
	resultCh  chan error
}

// LogBatcher batches logs before sending to Splunk
type LogBatcher struct {
	maxBatchSize int
	maxWaitTime  time.Duration
	batches      map[string]*Batch // key: token
	mu           sync.Mutex
	plugin       *K8sSplunkPlugin
}

// Batch holds logs for a specific token
type Batch struct {
	token      string
	events     []SplunkEvent
	firstAdded time.Time
	mu         sync.Mutex
}

// NewWorkerPool creates a worker pool
func NewWorkerPool(workers int, p *K8sSplunkPlugin) *WorkerPool {
	return &WorkerPool{
		workers:  workers,
		jobs:     make(chan *LogJob, 10000), // Large buffer for high throughput
		stopChan: make(chan struct{}),
		plugin:   p,
	}
}

// Start launches worker goroutines
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	log.Printf("[k8s_splunk] Started %d workers", wp.workers)
}

// worker processes jobs from the queue
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for {
		select {
		case job := <-wp.jobs:
			err := wp.plugin.processRecordBatched(job.timestamp, job.record)
			job.resultCh <- err

		case <-wp.stopChan:
			log.Printf("[k8s_splunk] Worker %d stopping", id)
			return
		}
	}
}

// Submit adds a job to the worker pool
func (wp *WorkerPool) Submit(job *LogJob) {
	wp.jobs <- job
}

// Stop gracefully shuts down workers
func (wp *WorkerPool) Stop() {
	close(wp.stopChan)
	wp.wg.Wait()
	log.Printf("[k8s_splunk] All workers stopped")
}

// NewLogBatcher creates a log batcher
func NewLogBatcher(maxBatchSize int, maxWaitTime time.Duration, p *K8sSplunkPlugin) *LogBatcher {
	b := &LogBatcher{
		maxBatchSize: maxBatchSize,
		maxWaitTime:  maxWaitTime,
		batches:      make(map[string]*Batch),
		plugin:       p,
	}

	// Start background flusher
	go b.periodicFlush()

	return b
}

// Add adds a log to the appropriate batch
func (b *LogBatcher) Add(token string, event SplunkEvent) error {
	b.mu.Lock()

	// Get or create batch for this token
	batch, exists := b.batches[token]
	if !exists {
		batch = &Batch{
			token:      token,
			events:     make([]SplunkEvent, 0, b.maxBatchSize),
			firstAdded: time.Now(),
		}
		b.batches[token] = batch
	}

	batch.mu.Lock()
	batch.events = append(batch.events, event)
	shouldFlush := len(batch.events) >= b.maxBatchSize
	batch.mu.Unlock()

	b.mu.Unlock()

	// Flush if batch is full
	if shouldFlush {
		return b.flushBatch(token)
	}

	return nil
}

// flushBatch sends a batch to Splunk
func (b *LogBatcher) flushBatch(token string) error {
	b.mu.Lock()
	batch, exists := b.batches[token]
	if !exists {
		b.mu.Unlock()
		return nil
	}

	// Take ownership of the batch
	batch.mu.Lock()
	events := batch.events
	batch.events = make([]SplunkEvent, 0, b.maxBatchSize)
	batch.firstAdded = time.Now()
	batch.mu.Unlock()

	b.mu.Unlock()

	// Send batch (outside locks)
	if len(events) == 0 {
		return nil
	}

	log.Printf("[k8s_splunk] Flushing batch: %d events for token %s", len(events), token[:10]+"...")

	// Retry logic for batch
	var lastErr error
	for attempt := 0; attempt <= b.plugin.config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := b.plugin.config.RetryBackoff * time.Duration(1<<uint(attempt-1))
			time.Sleep(backoff)
		}

		err := b.plugin.splunkClient.SendBatch(token, events)
		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("[k8s_splunk] Batch send attempt %d/%d failed: %v",
			attempt+1, b.plugin.config.MaxRetries+1, err)
	}

	return fmt.Errorf("batch failed after %d retries: %w", b.plugin.config.MaxRetries+1, lastErr)
}

// periodicFlush flushes old batches periodically
func (b *LogBatcher) periodicFlush() {
	ticker := time.NewTicker(b.maxWaitTime)
	defer ticker.Stop()

	for range ticker.C {
		b.mu.Lock()
		tokensToFlush := make([]string, 0)

		for token, batch := range b.batches {
			batch.mu.Lock()
			shouldFlush := time.Since(batch.firstAdded) >= b.maxWaitTime && len(batch.events) > 0
			batch.mu.Unlock()

			if shouldFlush {
				tokensToFlush = append(tokensToFlush, token)
			}
		}
		b.mu.Unlock()

		// Flush old batches
		for _, token := range tokensToFlush {
			if err := b.flushBatch(token); err != nil {
				log.Printf("[k8s_splunk] Periodic flush error: %v", err)
			}
		}
	}
}

// FlushAll flushes all pending batches
func (b *LogBatcher) FlushAll() {
	b.mu.Lock()
	tokens := make([]string, 0, len(b.batches))
	for token := range b.batches {
		tokens = append(tokens, token)
	}
	b.mu.Unlock()

	for _, token := range tokens {
		b.flushBatch(token)
	}
}

// FLBPluginInit - Initialize with improvements
//
//export FLBPluginInit
func FLBPluginInit(ctx unsafe.Pointer) int {
	config := &PluginConfig{
		SplunkURL:       output.FLBPluginConfigKey(ctx, "splunk_url"),
		SplunkIndex:     output.FLBPluginConfigKey(ctx, "splunk_index"),
		SecretLabelKey:  output.FLBPluginConfigKey(ctx, "secret_label_key"),
		SecretNamespace: output.FLBPluginConfigKey(ctx, "secret_namespace"),
		SecretKey:       output.FLBPluginConfigKey(ctx, "secret_key"),
		CacheTTL:        time.Hour,
		MaxRetries:      3,
		RetryBackoff:    time.Second,
	}

	// Parse optional settings
	if skipVerify := output.FLBPluginConfigKey(ctx, "insecure_skip_verify"); skipVerify == "true" || skipVerify == "on" {
		config.InsecureSkipVerify = true
	}

	// Validate required configuration
	if config.SplunkURL == "" {
		log.Printf("[k8s_splunk] ERROR: splunk_url is required")
		return output.FLB_ERROR
	}

	if config.SecretLabelKey == "" {
		config.SecretLabelKey = "splunk-token-secret"
	}

	if config.SecretKey == "" {
		config.SecretKey = "token"
	}

	// Initialize components
	k8sRetriever, err := NewK8sSecretRetriever(config.SecretNamespace)
	if err != nil {
		log.Printf("[k8s_splunk] ERROR: Failed to initialize Kubernetes client: %v", err)
		return output.FLB_ERROR
	}

	splunkClient := NewSplunkHECClient(config.SplunkURL, config.SplunkIndex, config.InsecureSkipVerify)
	secretCache := NewSecretCache(config.CacheTTL)

	// Create plugin instance
	plugin = &K8sSplunkPlugin{
		secretCache:  secretCache,
		splunkClient: splunkClient,
		k8sRetriever: k8sRetriever,
		config:       config,
	}

	// Initialize worker pool (50 workers for high throughput)
	plugin.workerPool = NewWorkerPool(50, plugin)
	plugin.workerPool.Start()

	// Initialize batcher (500 logs per batch, 1 second max wait)
	plugin.batcher = NewLogBatcher(500, 1*time.Second, plugin)

	log.Printf("[k8s_splunk] Initialized with 50 workers and batching")
	log.Printf("[k8s_splunk] Splunk URL: %s, Secret Label: %s", config.SplunkURL, config.SecretLabelKey)

	return output.FLB_OK
}

// FLBPluginFlushCtx - Process logs with worker pool
//
//export FLBPluginFlushCtx
func FLBPluginFlushCtx(ctx, data unsafe.Pointer, length C.int, tag *C.char) int {
	if plugin == nil {
		log.Printf("[k8s_splunk] ERROR: Plugin not initialized")
		return output.FLB_ERROR
	}

	dec := output.NewDecoder(data, int(length))
	var jobs []*LogJob

	// Collect all records
	for {
		ret, ts, record := output.GetRecord(dec)
		if ret != 0 {
			break
		}

		job := &LogJob{
			timestamp: ts,
			record:    record,
			resultCh:  make(chan error, 1),
		}
		jobs = append(jobs, job)

		// Submit to worker pool (non-blocking with buffer)
		plugin.workerPool.Submit(job)
	}

	// Wait for all jobs to complete
	sentCount := 0
	failedCount := 0

	for _, job := range jobs {
		err := <-job.resultCh
		if err != nil {
			failedCount++
			// Only log first few errors to avoid spam
			if failedCount <= 5 {
				log.Printf("[k8s_splunk] ERROR: %v", err)
			}
		} else {
			sentCount++
		}
	}

	log.Printf("[k8s_splunk] Processed %d records (sent: %d, failed: %d)",
		len(jobs), sentCount, failedCount)

	if sentCount > 0 {
		return output.FLB_OK
	}
	return output.FLB_RETRY
}

// processRecordBatched - Add to batch instead of immediate send
func (p *K8sSplunkPlugin) processRecordBatched(timestamp interface{}, record map[interface{}]interface{}) error {
	// Extract metadata
	k8sMetadata, err := extractK8sMetadata(record)
	if err != nil {
		return fmt.Errorf("failed to extract Kubernetes metadata: %w", err)
	}

	// Get secret name
	secretName, err := getSecretNameFromLabels(k8sMetadata, p.config.SecretLabelKey)
	if err != nil {
		return fmt.Errorf("failed to get secret name from labels: %w", err)
	}

	// Get token (cached)
	token, err := p.getSplunkToken(secretName, k8sMetadata.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Splunk token: %w", err)
	}

	// Convert to JSON-serializable format
	jsonRecord := convertToStringMap(record)

	// Create event
	event := SplunkEvent{
		Time:       p.splunkClient.formatTimestamp(timestamp),
		Event:      jsonRecord,
		Index:      p.config.SplunkIndex,
		Host:       k8sMetadata.PodName,
		Source:     fmt.Sprintf("%s/%s", k8sMetadata.Namespace, k8sMetadata.PodName),
		SourceType: "kubernetes:pod",
	}

	// Add to batch (will auto-flush when full)
	return p.batcher.Add(token, event)
}

// getSplunkToken - Same as before but in improved plugin
func (p *K8sSplunkPlugin) getSplunkToken(secretName, namespace string) (string, error) {
	cacheKey := fmt.Sprintf("%s/%s", namespace, secretName)

	if token, found := p.secretCache.Get(cacheKey); found {
		return token, nil
	}

	if namespace == "" {
		namespace = p.config.SecretNamespace
	}

	token, err := p.k8sRetriever.GetSecret(namespace, secretName, p.config.SecretKey)
	if err != nil {
		return "", fmt.Errorf("failed to fetch secret %s/%s: %w", namespace, secretName, err)
	}

	p.secretCache.Set(cacheKey, token)
	return token, nil
}

// Helper functions (reused from original)
func extractK8sMetadata(record map[interface{}]interface{}) (*K8sMetadata, error) {
	k8sData, ok := record["kubernetes"].(map[interface{}]interface{})
	if !ok {
		return nil, fmt.Errorf("kubernetes metadata not found in record")
	}

	metadata := &K8sMetadata{}

	if ns, ok := k8sData["namespace_name"].(string); ok {
		metadata.Namespace = ns
	} else if ns, ok := k8sData["namespace_name"].([]byte); ok {
		metadata.Namespace = string(ns)
	}

	if pod, ok := k8sData["pod_name"].(string); ok {
		metadata.PodName = pod
	} else if pod, ok := k8sData["pod_name"].([]byte); ok {
		metadata.PodName = string(pod)
	}

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
		return nil, fmt.Errorf("required Kubernetes metadata not found")
	}

	return metadata, nil
}

func getSecretNameFromLabels(metadata *K8sMetadata, secretLabelKey string) (string, error) {
	secretName, ok := metadata.Labels[secretLabelKey]
	if !ok || secretName == "" {
		return "", fmt.Errorf("secret label '%s' not found in pod labels", secretLabelKey)
	}
	return secretName, nil
}

// FLBPluginExit - Clean shutdown
//
//export FLBPluginExit
func FLBPluginExit() int {
	if plugin != nil {
		log.Printf("[k8s_splunk] Shutting down...")

		// Flush pending batches
		if plugin.batcher != nil {
			plugin.batcher.FlushAll()
		}

		// Stop workers
		if plugin.workerPool != nil {
			plugin.workerPool.Stop()
		}

		// Close HTTP client
		if plugin.splunkClient != nil {
			plugin.splunkClient.Close()
		}
	}

	log.Printf("[k8s_splunk] Plugin exited gracefully")
	return output.FLB_OK
}

func main() {
	// Required for CGo plugins
}
