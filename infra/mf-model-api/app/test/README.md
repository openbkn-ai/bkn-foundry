# MF-Model-API Test Documentation

## Overview

This test suite provides comprehensive unit tests for the `mf-model-api` project, with a target coverage of at least 75%.

## Test Structure

```
app/test/
├── __init__.py                     # Test package initialization
├── conftest.py                     # pytest configuration and shared fixtures
├── test_commons_response.py        # Tests for the response module
├── test_commons_snow_id.py         # Tests for the snow_id module
├── test_core_config.py             # Tests for the configuration module
├── test_dao_llm_model_dao.py       # Tests for the DAO layer
├── test_utils_str_util.py          # Tests for string utilities
├── test_utils_comment_utils.py     # Tests for logging utilities
├── test_utils_param_verify.py      # Tests for parameter validation
├── test_restful_api.py             # Tests for RESTful API document generation
├── test_controller_llm.py          # Tests for the LLM controller
├── test_routers.py                 # Tests for routers
├── test_app_utils.py               # Tests for application utilities
└── README.md                       # This file
```

## Installing Dependencies

```bash
pip install -r requirements-test.txt
```

## Running Tests

### Run all tests

```bash
pytest
```

### Run tests for a specific module

```bash
pytest app/test/test_commons_snow_id.py
```

### Generate a coverage report

```bash
pytest --cov=app --cov-report=html --cov-report=term-missing
```

### View the HTML coverage report

```bash
# Open in a browser
htmlcov/index.html
```

## Covered Modules

### Commons Module

- ✅ `response.py` - Response handling functions
- ✅ `snow_id.py` - Snowflake ID generator
- ✅ `restful_api.py` - RESTful API document generation

### Utils Module

- ✅ `str_util.py` - String utility functions
- ✅ `comment_utils.py` - Logging utility functions
- ✅ `param_verify_utils.py` - Parameter validation functions
- ✅ `app_utils.py` - Application utility functions

### Core Module

- ✅ `config.py` - Configuration management

### DAO Module

- ✅ `llm_model_dao.py` - LLM model data access

### Controller Module

- ✅ `llm_controller.py` - LLM controller logic

### Routers Module

- ✅ `llm_router.py` - LLM routes

## Test Coverage Targets

- **Overall coverage**: ≥ 75%
- **Critical module coverage**: ≥ 80%
  - Commons: ≥ 80%
  - Utils: ≥ 80%
  - Controller: ≥ 75%

## Test Types

### Unit Tests

Test individual functions or classes and isolate dependencies with mocks.

### Integration Tests

Test interactions between multiple modules.

### Asynchronous Tests

Use `pytest-asyncio` to test asynchronous functions.

## FAQ

### Q: How do I mock database connections?

A: Use the `mock_db_connection` fixture in `conftest.py`.

### Q: How do I mock Redis?

A: Use the `mock_redis` fixture in `conftest.py`.

### Q: How do I test asynchronous functions?

A: Use the `@pytest.mark.asyncio` decorator.

## Mocking Strategy

1. **Database**: Use mock objects for database connections and cursors.
2. **Redis**: Use `AsyncMock` for Redis operations.
3. **HTTP requests**: Use `aioresponses` or patch `aiohttp.ClientSession`.
4. **Environment variables**: Use `patch.dict` to mock environment variables.

## Continuous Integration

Tests can be integrated into the CI/CD process:

```yaml
# Example .github/workflows/test.yml
- name: Run tests
  run: |
    pip install -r requirements-test.txt
    pytest --cov=app --cov-report=xml

- name: Upload coverage
  uses: codecov/codecov-action@v3
  with:
    file: ./coverage.xml
```

## Contribution Guidelines

1. New features must include corresponding tests.
2. Test coverage must not fall below 75%.
3. All tests must pass before merge.
4. Follow the existing test naming and structure conventions.

## Contact

Contact the development team if you have questions.
