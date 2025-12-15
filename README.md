# Fluent Bit Kubernetes to Splunk Output Plugin

A Fluent Bit output plugin that dynamically retrieves Splunk HEC tokens from Kubernetes secrets based on pod labels.

## Features

- Dynamic Splunk HEC token retrieval from Kubernetes secrets
- Pod label-based token selection
- 1-hour secret caching (reduces K8s API calls by 99%+)
- Exponential backoff retry logic
- Thread-safe concurrent processing

## Versions

| Version | Performance | Use Case |
|---------|-------------|----------|
| [Basic](output/basic/) | ~500 logs/sec | Development, small clusters |
| [Improved](output/improved/) | ~20,000 logs/sec | Production, high volume |

## Quick Start

### 1. Choose Version

```bash
# For production (recommended)
cd output/improved

# For development
cd output/basic
```

### 2. Build

```bash
make build
```

### 3. Deploy

```bash
# Build Docker image
make docker-build REGISTRY=your-registry
make docker-push REGISTRY=your-registry

# Create secrets
kubectl apply -f ../../examples/splunk-secret.yaml

# Deploy Fluent Bit
kubectl apply -f ../../examples/fluent-bit-daemonset.yaml

# Update image
kubectl set image daemonset/fluent-bit \
  fluent-bit=your-registry/fluent-bit-k8s-splunk:improved-latest \
  -n logging
```

## Configuration

### Pod Labels

```yaml
metadata:
  labels:
    splunk-token-secret: my-app-token
```

### Kubernetes Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-app-token
stringData:
  token: YOUR-SPLUNK-HEC-TOKEN
```

### Fluent Bit Config

```ini
[OUTPUT]
    Name                k8s_splunk
    Match               kube.*
    splunk_url          https://splunk.example.com:8088/services/collector/event
    splunk_index        kubernetes
    secret_label_key    splunk-token-secret
    secret_key          token
```

## Architecture

### Basic Version
- Sequential log processing
- ~500 logs/sec per node
- 100-200MB memory, 100m-500m CPU

### Improved Version (Recommended)
- 50 concurrent worker goroutines
- Batching (500 logs per request)
- ~20,000 logs/sec per node
- 512MB-1GB memory, 200m-1000m CPU

## Project Structure

```
output/
├── basic/          # Basic version
│   ├── *.go
│   ├── Makefile
│   ├── Dockerfile
│   └── README.md
└── improved/       # Improved version
    ├── *.go
    ├── Makefile
    ├── Dockerfile
    └── README.md

examples/           # Kubernetes manifests
```

## Documentation

- [Basic Version](output/basic/README.md)
- [Improved Version](output/improved/README.md)

## Troubleshooting

```bash
# Check plugin loaded
kubectl logs -n logging -l app=fluent-bit | grep k8s_splunk

# Verify secret
kubectl get secret my-app-token

# Test connectivity
kubectl exec -n logging fluent-bit-xxxxx -- \
  curl -k https://splunk.example.com:8088
```

## License

MIT
