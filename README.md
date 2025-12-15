# Fluent Bit Kubernetes to Splunk Output Plugin

A Fluent Bit output plugin that dynamically retrieves Splunk HEC tokens from Kubernetes secrets based on pod labels.

## Features

- Dynamic Splunk HEC token retrieval from Kubernetes secrets
- Pod label-based token and index selection
- Per-pod Splunk index routing via labels
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

## Usage

### Step 1: Create Splunk HEC Tokens

In Splunk, create one or more HEC tokens:

1. Go to **Settings** → **Data Inputs** → **HTTP Event Collector**
2. Click **New Token**
3. Configure token settings and allowed indexes
4. Copy the generated token

### Step 2: Create Kubernetes Secrets

Create a secret for each HEC token:

```bash
kubectl create secret generic my-app-token \
  --from-literal=token=YOUR-SPLUNK-HEC-TOKEN \
  -n default
```

Or use YAML:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-app-token
  namespace: default
type: Opaque
stringData:
  token: YOUR-SPLUNK-HEC-TOKEN
```

### Step 3: Label Your Pods

Add labels to your pods to specify which token and index to use:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
  labels:
    app: my-app
    splunk-token-secret: my-app-token  # REQUIRED: Secret name
    splunk-index: app-logs             # OPTIONAL: Override default index
spec:
  containers:
  - name: app
    image: my-app:latest
```

### Step 4: Configure Fluent Bit

Add the k8s_splunk output to your Fluent Bit configuration:

```ini
[OUTPUT]
    Name                k8s_splunk
    Match               kube.*
    splunk_url          https://splunk.example.com:8088/services/collector/event
    splunk_index        kubernetes              # Default index (optional)
    secret_label_key    splunk-token-secret     # Pod label for secret name
    index_label_key     splunk-index            # Pod label for index (optional)
    secret_key          token                   # Key name in K8s secret
    insecure_skip_verify off                    # Set to 'on' for self-signed certs
```

**Configuration Parameters:**

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `splunk_url` | Yes | - | Splunk HEC endpoint URL |
| `splunk_index` | No | - | Default index if not in pod labels |
| `secret_label_key` | No | `splunk-token-secret` | Pod label key for secret name |
| `index_label_key` | No | `splunk-index` | Pod label key for index name |
| `secret_key` | No | `token` | Key name in Kubernetes secret |
| `secret_namespace` | No | Pod's namespace | Namespace to search for secrets |
| `insecure_skip_verify` | No | `off` | Skip TLS verification |

### Step 5: Deploy

```bash
# Build plugin
cd output/improved
make build

# Build and push Docker image
make docker-build REGISTRY=your-registry
make docker-push REGISTRY=your-registry

# Deploy Fluent Bit with the plugin
kubectl apply -f ../../examples/fluent-bit-daemonset.yaml
```

## Configuration Examples

### Example 1: Simple Setup (One Token, One Index)

All pods use the same token and index:

```yaml
# Secret
apiVersion: v1
kind: Secret
metadata:
  name: default-splunk-token
stringData:
  token: abc123-token-here

---
# Pod
apiVersion: v1
kind: Pod
metadata:
  labels:
    splunk-token-secret: default-splunk-token
```

Fluent Bit sends logs to the default index configured in `splunk_index`.

### Example 2: Multiple Applications, Different Indexes

Different apps route to different indexes using the same token:

```yaml
# App 1 Pod
metadata:
  labels:
    splunk-token-secret: default-splunk-token
    splunk-index: app1-logs

---
# App 2 Pod
metadata:
  labels:
    splunk-token-secret: default-splunk-token
    splunk-index: app2-logs
```

### Example 3: Multiple Teams, Different Tokens

Different teams use their own HEC tokens:

```yaml
# Team A Secret
apiVersion: v1
kind: Secret
metadata:
  name: team-a-token
stringData:
  token: team-a-hec-token

---
# Team A Pod
metadata:
  labels:
    splunk-token-secret: team-a-token
    splunk-index: team-a-logs

---
# Team B Secret
apiVersion: v1
kind: Secret
metadata:
  name: team-b-token
stringData:
  token: team-b-hec-token

---
# Team B Pod
metadata:
  labels:
    splunk-token-secret: team-b-token
    splunk-index: team-b-logs
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
