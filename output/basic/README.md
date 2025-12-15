# Basic Version

Performance: **~500 logs/sec per node**

## When to Use

- Development and testing
- Small clusters (< 20 nodes)
- Low log volume
- Simple requirements

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
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

## Architecture

Sequential log processing - processes one log at a time.

## Upgrade

Need more performance? Use the [improved version](../improved/).
