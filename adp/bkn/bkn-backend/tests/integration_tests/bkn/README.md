# BKN Import/Export Integration Tests

Verify BKN file import, export, and data consistency for tar-format BKN files.

## Technology Stack

- **Language**: Go 1.24
- **Test framework**: [GoConvey](https://github.com/smartystreets/goconvey) (BDD style)
- **Configuration management**: [Viper](https://github.com/spf13/viper) (YAML + environment variables)

## Directory Structure

```
bkn/
├── bkn_test.go                               # Main test file (10 cases)
├── README.md
└── helpers/
    ├── bkn_helpers.go                        # Test helper functions
    └── examples/                             # Test data
        └── k8s-network/                      # K8s network topology example
            ├── CHECKSUM
            ├── SKILL.md
            ├── network.bkn
            ├── concept_groups/k8s.bkn
            ├── object_types/                 # node, pod, service
            ├── relation_types/               # pod_belongs_node, service_routes_pod
            ├── action_types/                 # restart_pod, cordon_node
            └── risk_types/                   # restart_pod_high_risk
```

## Running Tests

### Configuration

The test configuration file is located at `../testdata/test-config.yaml`. It can also be overridden with environment variables that use the `BKN_TEST_` prefix:

```bash
export BKN_TEST_BKN_BACKEND_BASE_URL=http://my-server:8080
```

### Execution

```bash
# Run from the tests directory.
cd bkn/bkn-backend/tests
go test ./integration_tests/bkn/ -v
```

## Test Case List

### Import Tests (BKN101-BKN102)

| ID | Description | Verification Points |
|----|-------------|---------------------|
| BKN101 | Import the k8s-network example | Tar builds successfully, upload returns 200, response contains kn_id |
| BKN102 | Verify all types are created after import | Import k8s-network, then GET object-types, relation-types, action-types, and concept-groups; each list is non-empty |

### Export Tests (BKN121-BKN122)

| ID | Description | Verification Points |
|----|-------------|---------------------|
| BKN121 | Basic export scenario | Returns 200, response body is non-empty, and `IsValidTar` passes |
| BKN122 | Verify Content-Disposition contains kn_id | Content-Disposition header is non-empty and contains knID |

### Negative Tests (BKN201-BKN204)

| ID | Description | Verification Points |
|----|-------------|---------------------|
| BKN201 | Import an invalid file format (plain text) | Returns >= 400 |
| BKN202 | Export a non-existent knowledge network | Returns >= 400 |
| BKN203 | Import an empty file | Returns >= 400 |
| BKN204 | Import a tar archive without network.bkn | Tar is valid but lacks the required file; returns >= 400 |

### Complex Data Test (BKN221)

| ID | Description | Verification Points |
|----|-------------|---------------------|
| BKN221 | Export a BKN with a complex structure | Exported content contains object_types, relation_types, and action_types bytes |

## Test Data

### k8s-network

Kubernetes cluster network topology containing 3 object types, 2 relation types, 2 action types, and 1 risk type.

```
Service ──routes──> Pod ──belongs──> Node
                     │                │
              restart_pod        cordon_node
              (action)           (action)
```

## Helper Functions (helpers/bkn_helpers.go)

| Function | Purpose |
|----------|---------|
| `IsValidTar(data)` | Verifies whether byte data is a valid tar archive |
| `GenerateUniqueName(prefix)` | Generates a unique timestamped name |
| `BuildStringWithLength(char, length)` | Builds a string with the specified length |
| `DeleteTestKN(client, knID, branch, t)` | Deletes a test knowledge network |
| `CleanupKNs(client, t)` | Cleans up all test KNs |
| `BuildTarFromExamplesDir(exampleName)` | Builds a tar archive from the examples directory |
| `BuildSimpleBKNTar(knID)` | Builds a simple tar archive (network.bkn + 1 object_type) |
| `BuildFullBKNTar(knID)` | Builds a full tar archive (2 object_types + 1 relation + 1 action) |
| `BuildTarWithoutNetworkBKN()` | Builds a tar archive without network.bkn for negative testing |
| `GetExampleNames()` | Gets the list of available example names |
| `CreateTestObjectType(client, knID, t)` | Creates a test object type through the API |
| `CreateTestRelationType(...)` | Creates a test relation type through the API |
| `CreateTestActionType(...)` | Creates a test action type through the API |
| `VerifyObjectTypesExist(client, knID, t)` | Verifies that object types exist |
| `VerifyRelationTypesExist(client, knID, t)` | Verifies that relation types exist |
| `VerifyActionTypesExist(client, knID, t)` | Verifies that action types exist |
| `VerifyConceptGroupsExist(client, knID, t)` | Verifies that concept groups exist |

## API Endpoint Coverage

| Endpoint | Method | Purpose | Test Coverage |
|----------|--------|---------|---------------|
| `/api/bkn-backend/v1/bkns` | POST | Import BKN | BKN101-102 |
| `/api/bkn-backend/v1/bkns/{knID}` | GET | Export BKN | BKN121-122, BKN221 |
| `/api/bkn-backend/v1/knowledge-networks/{knID}/object-types` | GET | List object types | BKN102 |
| `/api/bkn-backend/v1/knowledge-networks/{knID}/relation-types` | GET | List relation types | BKN102 |
| `/api/bkn-backend/v1/knowledge-networks/{knID}/action-types` | GET | List action types | BKN102 |
| `/api/bkn-backend/v1/knowledge-networks/{knID}/concept-groups` | GET | List concept groups | BKN102 |
| `/api/bkn-backend/v1/knowledge-networks` | GET | List KNs | Cleanup (teardown) |
| `/api/bkn-backend/v1/knowledge-networks/{knID}` | DELETE | Delete a KN | Cleanup (teardown) |

## Suggested Additional Test Scenarios

| Scenario | Priority | Description |
|----------|----------|-------------|
| Idempotency | High | Import the same BKN twice and verify that no error or duplicate data is produced |
| Overwrite/update semantics | Medium | Import an existing KN again and verify that data is updated instead of duplicated |
| Default branch behavior | Medium | Verify default behavior when the branch parameter is omitted |
