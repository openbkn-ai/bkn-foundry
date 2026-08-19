# Sandbox Executor

> A secure code execution daemon that uses Bubblewrap and macOS Seatbelt for process-level isolation

[![Python Version](https://img.shields.io/badge/python-3.11+-blue.svg)](https://www.python.org/downloads/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## Overview

Sandbox Executor is a high-performance code execution service designed for AI Agent scenarios. It provides multilayer security isolation to ensure untrusted code runs safely in a controlled environment.

## Key features

- **Multilayer security isolation** - Docker container plus Bubblewrap/sandbox-exec isolation
- **High-performance async execution** - Real async execution based on asyncio with high-concurrency support
- **Lambda compatible** - Supports the AWS Lambda handler convention
- **Real-time observability** - Heartbeats, lifecycle management, and execution metrics

## Quick Start

```bash
# Install dependencies
pip install -r requirements.txt

# Start the service
python -m executor.interfaces.http.rest

# validate the service
curl http://localhost:8080/health
```

**Detailed guide**: [Quick Start documentation](docs/quick-start.md)

## Technology stack

| Component | Technology |
|------|------|
| HTTP framework | FastAPI + Uvicorn |
| Isolation technology | Bubblewrap (Linux) / sandbox-exec (macOS) |
| Async runtime | asyncio |
| Logging | structlog |
| Data validation | Pydantic |

## Documentation

| Document | Description |
|------|------|
| [Quick Start](docs/quick-start.md) | installation, configuration, and basic usage |
| [Architecture design](docs/architecture.md) | hexagonal architecture, module structure, and design principles |
| [API documentation](docs/api-reference.md) | RESTful API endpoints and examples |
| [configuration guide](docs/configuration.md) | environment variables and isolation configuration |
| [Development guide](docs/development.md) | development setup, tests, and code conventions |
| [deployment guide](docs/deployment.md) | Docker, Docker Compose, and Kubernetes deployment |
| [Troubleshooting](docs/troubleshooting.md) | common issues and solutions |

## Examples

### Python Handler

```python
def handler(event):
    name = event.get('name', 'World')
    return {'message': f'Hello, {name}!'}
```

### Execute code

```bash
curl -X POST http://localhost:8080/execute \
  -H 'Content-Type: application/json' \
  -d '{
    "execution_id": "test_001",
    "session_id": "session_001",
    "code": "def handler(event): return {\"message\": \"Hello!\"}",
    "language": "python",
    "timeout": 10
  }'
```

## License

MIT License - see [LICENSE](../../LICENSE).
