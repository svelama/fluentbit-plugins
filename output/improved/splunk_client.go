package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SplunkHECClient handles communication with Splunk HTTP Event Collector
type SplunkHECClient struct {
	url        string
	index      string
	httpClient *http.Client
}

// SplunkEvent represents the structure of a Splunk HEC event
type SplunkEvent struct {
	Time       interface{}            `json:"time"`
	Event      interface{}            `json:"event"`
	Source     string                 `json:"source,omitempty"`
	SourceType string                 `json:"sourcetype,omitempty"`
	Index      string                 `json:"index,omitempty"`
	Host       string                 `json:"host,omitempty"`
}

// NewSplunkHECClient creates a new Splunk HEC client
func NewSplunkHECClient(url, index string, insecureSkipVerify bool) *SplunkHECClient {
	// Create HTTP client with custom transport
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecureSkipVerify,
		},
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	return &SplunkHECClient{
		url:        url,
		index:      index,
		httpClient: httpClient,
	}
}

// SendEvent sends a log event to Splunk HEC
func (s *SplunkHECClient) SendEvent(token string, timestamp interface{}, record map[interface{}]interface{}, metadata *K8sMetadata) error {
	// Convert record to JSON-serializable format
	jsonRecord := convertToStringMap(record)

	// Construct Splunk event
	event := SplunkEvent{
		Time:  s.formatTimestamp(timestamp),
		Event: jsonRecord,
		Index: s.index,
	}

	// Add host from Kubernetes metadata if available
	if metadata != nil && metadata.PodName != "" {
		event.Host = metadata.PodName
	}

	// Set source and sourcetype from Kubernetes metadata
	if metadata != nil && metadata.Namespace != "" {
		event.Source = fmt.Sprintf("%s/%s", metadata.Namespace, metadata.PodName)
		event.SourceType = "kubernetes:pod"
	}

	// Marshal event to JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event to JSON: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", s.url, bytes.NewBuffer(eventJSON))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Splunk "+token)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to Splunk: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Splunk HEC returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read response body to ensure connection is reused
	io.Copy(io.Discard, resp.Body)

	return nil
}

// SendBatch sends multiple events to Splunk HEC in a single request
// This is more efficient when processing multiple records
func (s *SplunkHECClient) SendBatch(token string, events []SplunkEvent) error {
	// Build JSON Lines format (one JSON object per line)
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)

	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("failed to encode event: %w", err)
		}
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", s.url, &buf)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Splunk "+token)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send batch request to Splunk: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Splunk HEC returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read response body to ensure connection is reused
	io.Copy(io.Discard, resp.Body)

	return nil
}

// formatTimestamp converts Fluent Bit timestamp to Unix epoch time
func (s *SplunkHECClient) formatTimestamp(timestamp interface{}) interface{} {
	switch ts := timestamp.(type) {
	case uint64:
		return ts
	case int64:
		return ts
	case float64:
		return ts
	case time.Time:
		return ts.Unix()
	default:
		// If timestamp format is unknown, use current time
		return time.Now().Unix()
	}
}

// Close closes the HTTP client and cleans up resources
func (s *SplunkHECClient) Close() {
	if s.httpClient != nil {
		s.httpClient.CloseIdleConnections()
	}
}

// convertToStringMap converts map[interface{}]interface{} to map[string]interface{}
// This is necessary because JSON encoding requires string keys
func convertToStringMap(input map[interface{}]interface{}) map[string]interface{} {
	output := make(map[string]interface{})
	for k, v := range input {
		// Convert key to string
		var key string
		switch k := k.(type) {
		case string:
			key = k
		case []byte:
			key = string(k)
		default:
			key = fmt.Sprintf("%v", k)
		}

		// Recursively convert nested maps
		switch v := v.(type) {
		case map[interface{}]interface{}:
			output[key] = convertToStringMap(v)
		case []interface{}:
			output[key] = convertSlice(v)
		default:
			output[key] = v
		}
	}
	return output
}

// convertSlice recursively converts slices containing map[interface{}]interface{}
func convertSlice(input []interface{}) []interface{} {
	output := make([]interface{}, len(input))
	for i, v := range input {
		switch v := v.(type) {
		case map[interface{}]interface{}:
			output[i] = convertToStringMap(v)
		case []interface{}:
			output[i] = convertSlice(v)
		default:
			output[i] = v
		}
	}
	return output
}
