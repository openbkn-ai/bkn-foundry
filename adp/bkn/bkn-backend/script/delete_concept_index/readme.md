# Create the Concept Index Deletion Script

## Overview

Create a Python script that deletes the `adp-kn_concept` concept index from OpenSearch. The script is placed under the `script/` directory.

## Implementation Details

### File Structure

- `script/delete_concept_index.py` - Main script file

### Features

1. **OpenSearch connection**

   - Follows the connection pattern from `script/clean_opensearch_index/script.py`
   - Supports HTTP/HTTPS protocols
   - Supports username/password authentication
   - Reads configuration from environment variables or command-line arguments

2. **Index deletion**

   - Deletes the `adp-kn_concept` concept index, which is defined as `KN_CONCEPT_INDEX_NAME` in `server/interfaces/common.go`
   - Checks whether the index exists before deletion
   - Displays index information, including document count and storage size

3. **Safety features**

   - Supports `--dry-run` mode to preview the operation without deleting the index
   - Displays index information before deletion and optionally asks for confirmation
   - Provides detailed log output

4. **Configuration methods**

   - Environment variables: `OPENSEARCH_HOST`, `OPENSEARCH_PORT`, `OPENSEARCH_PROTOCOL`, `OPENSEARCH_USER`, `OPENSEARCH_PASSWORD`
   - Command-line arguments override environment variables
   - Defaults: localhost:9200, http protocol

## Environment Requirements

- Python 3.10+
- OpenSearch cluster

## Install Dependencies

```bash
pip3 install -i https://pypi.tuna.tsinghua.edu.cn/simple opensearch-py
```

### Usage Examples

```bash
# Use environment variables.
export OPENSEARCH_HOST=localhost
export OPENSEARCH_PORT=9200
export OPENSEARCH_USER=test
export OPENSEARCH_PASSWORD=testpwd
python3 script/delete_concept_index.py

# Use command-line arguments.
python3 script/delete_concept_index.py --os-host localhost --os-port 9200 --os-user test --os-password testpwd

# Dry-run mode.
python3 script/delete_concept_index.py --dry-run
```

## References

- `script/clean_opensearch_index/script.py` - OpenSearch connection and index deletion implementation
- `server/interfaces/common.go` - Concept index name definition (`KN_CONCEPT_INDEX_NAME = "adp-kn_concept"`)
- `server/common/setting.go` - OpenSearch configuration structure
