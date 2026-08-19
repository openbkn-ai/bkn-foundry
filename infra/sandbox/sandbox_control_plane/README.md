# Sandbox Control Plane

A code sandbox management platform based on hexagonal architecture.

## Architecture

This project uses **Hexagonal Architecture** to keep core business logic independent from technical implementations.

### Dependency direction

```
Interfaces → Application → Domain ← Infrastructure
```

- **Domain**: Core business logic without external dependencies
- **Application**: Use-case orchestration; depends on Domain
- **Infrastructure**: Technical implementations for interfaces defined by Domain
- **Interfaces**: External interfaces that depend on Application

## Quick Start

### Installation

```bash
# Clone the repository
git clone https://github.com/sandbox/sandbox-control-plane
cd sandbox-control-plane

# Use uv to manage the project
# Install uv if it is not installed yet
# curl -LsSf https://astral.sh/uv/install.sh | sh  # Linux/macOS
# powershell -c "irm https://astral.sh/uv/install.ps1"  # Windows

# Sync dependencies
uv sync

# Activate the virtual environment
source .venv/bin/activate  # Linux/macOS
.venv\Scripts\activate     # Windows
```

### configuration

```bash
# Copy the environment variable template
cp .env.example .env

# Edit configuration
vim .env
```

### Database migration

```bash
# Initialize the database
alembic upgrade head
```

### Run

```bash
# Development mode
uvicorn src.interfaces.rest.main:app --reload --port 8000

# Production mode
uvicorn src.interfaces.rest.main:app --host 0.0.0.0 --port 8000 --workers 4
```

## Project structure

```
src/
├── domain/           # Domain layer (core)
├── application/      # Application layer (use cases)
├── infrastructure/   # Infrastructure layer (adapters)
└── interfaces/       # Interface layer (API)
```

See [PROJECT_STRUCTURE.md](PROJECT_STRUCTURE.md).

## API documentation

After starting the service, visit:

- Swagger UI: http://localhost:8000/docs
- ReDoc: http://localhost:8000/redoc

## Test

```bash
# Run all tests with uv
uv run pytest

# Run unit tests
uv run pytest tests/unit

# Run integration tests
uv run pytest tests/integration

# Generate a coverage report
uv run pytest --cov=src --cov-report=html
```

## Development

```bash
# Run code formatting with uv
uv run black src tests

# Code linting
uv run ruff check src tests

# Type checking
uv run mypy src
```

## License

MIT License
