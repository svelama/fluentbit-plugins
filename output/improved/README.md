# Improved Version

Performance: **~20,000 logs/sec per node** (40x faster than basic)

## When to Use

- Production deployments
- High log volume (500-20K logs/sec)
- Medium to large clusters

## Features

- 50 concurrent worker goroutines
- Batching (500 logs per request)
- Smart buffering (10,000 job queue)
- Auto-flush (1 second max wait)

## Build

```bash
make build
```

## Docker

```bash
make docker-build REGISTRY=your-registry
make docker-push REGISTRY=your-registry
```

## Resource Requirements

```yaml
resources:
  requests:
    cpu: 200m
    memory: 512Mi
  limits:
    cpu: 1000m
    memory: 1Gi
```

## Performance Tuning

Edit `out_k8s_splunk.go`:

```go
// Adjust workers (line ~350)
NewWorkerPool(50, pluginImproved)  // Default: 50

// Adjust batch size (line ~355)
NewLogBatcher(500, 1*time.Second, pluginImproved)  // Default: 500 logs
```

## Usage

### Quick Start

1. **Build the plugin:**

   ```bash
   make build
   ```

2. **Configure Fluent Bit:**

   ```ini
   [OUTPUT]
       Name                k8s_splunk
       Match               kube.*
       splunk_url          https://splunk.example.com:8088/services/collector/event
       splunk_index        kubernetes
       secret_label_key    splunk-token-secret
       index_label_key     splunk-index
       secret_key          token
   ```

3. **Label your pods:**

   ```yaml
   metadata:
     labels:
       splunk-token-secret: my-app-token  # Secret name
       splunk-index: app-logs             # Optional index override
   ```

4. **Create secrets:**

   ```bash
   kubectl create secret generic my-app-token \
     --from-literal=token=YOUR-HEC-TOKEN \
     -n default
   ```

### Integration

**Dockerfile example:**

```dockerfile
FROM fluent/fluent-bit:latest

# Copy plugin
COPY out_k8s_splunk.so /fluent-bit/plugins/

# Configure to load plugin
ENV FLB_PLUGINS_PATH=/fluent-bit/plugins
```

**Kubernetes ConfigMap:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluent-bit-config
data:
  fluent-bit.conf: |
    [SERVICE]
        Flush        1
        Daemon       off
        Log_Level    info

    [INPUT]
        Name              tail
        Path              /var/log/containers/*.log
        Parser            docker
        Tag               kube.*
        Refresh_Interval  5

    [FILTER]
        Name                kubernetes
        Match               kube.*
        Kube_URL            https://kubernetes.default.svc:443
        Kube_CA_File        /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        Kube_Token_File     /var/run/secrets/kubernetes.io/serviceaccount/token
        Merge_Log           on
        Keep_Log            off

    [OUTPUT]
        Name                k8s_splunk
        Match               kube.*
        splunk_url          https://splunk.example.com:8088/services/collector/event
        splunk_index        kubernetes
        secret_label_key    splunk-token-secret
        index_label_key     splunk-index
        secret_key          token
```

## Monitoring

### Watch Logs

```bash
# Watch batching in action
kubectl logs -n logging -l app=fluent-bit -f | grep "Flushing batch"

# Example output:
# [k8s_splunk] Flushing batch: 500 events for token abc123...
# [k8s_splunk] Processed 1247 records (sent: 1247, failed: 0)
```

### Metrics to Monitor

```bash
# Check plugin initialization
kubectl logs -n logging fluent-bit-xxxxx | grep "Initialized with"
# Output: [k8s_splunk] Initialized with 50 workers and batching

# Monitor batch sizes
kubectl logs -n logging fluent-bit-xxxxx | grep "Flushing batch" | tail -20

# Check for errors
kubectl logs -n logging fluent-bit-xxxxx | grep "ERROR"

# Verify secret caching (should see reduced K8s API calls)
kubectl logs -n logging fluent-bit-xxxxx | grep "failed to fetch secret"
```

## Architecture

- Worker pool processes logs concurrently
- Logs are batched by HEC token only (500 per batch)
- Events within same batch can have different indexes
- Batches auto-flush after 1 second or when full
- Result: 20,000 logs/sec → ~40 HTTP requests/sec to Splunk

**Batching Strategy:**

- Same token + any indexes → batched together ✅
- Different tokens → separate batches
- Splunk HEC natively supports per-event index within single request
