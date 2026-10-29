# Dependencies

Public model APIs validate the current bkn-safe account state after OAuth token
introspection. Disabled or missing accounts receive `401`; an unavailable account
lookup returns `503` without entering the business handler. Private service APIs
retain their internal identity contract.

Large- and small-model configuration tests require `execute` authorization before
loading saved configuration or contacting a provider. New configurations use the
model type's wildcard resource. Public default small-model reads require `display`
authorization; authorized callers still receive `200 {}` when no default exists.

Focused regression tests (no database, cache, or provider service required):

```bash
python3 -m pytest app/test/test_disabled_account_access.py -q
```

1. Install the Python libraries listed in `requirements.txt`: \
     pip install -r requirements.txt
2. Install `llmadapter-1.0.3-py3-none-any.whl`: \
     change to the directory containing the `.whl` file, then run: \
     pip install llmadapter-1.0.3-py3-none-any.whl -i https://pypi.tuna.tsinghua.edu.cn/simple

## Reranker document limit

Small-model create, edit and connection-test requests accept `max_documents` for
reranker models only. It is an optional integer from 1 through 2147483647, separate
from `batch_size` and token limits. List, detail, name and default-model reads return
`max_documents`; `null` means no local document-count limit. Existing provider
limits still apply. Creation without the field is unlimited locally; editing without
it preserves the saved value, while explicit `null` clears it. Embedding requests
must omit it or send `null`.

`mf-model-api` rejects a rerank request exceeding the saved limit with HTTP 400 and
`ModelFactory.ExternalSmallModel.Used.InvalidParameter` before invoking either a
provider or an adapter. It does not truncate or split documents. Connection tests
probe connectivity with one document; they do not certify provider capacity.

The schema prerequisite is `migrations/mf-model-manager/mariadb/0.1.6`: fresh installs
use its complete `init.sql`, and upgrades use `01-reranker-max-documents.sql`.
Existing models retain `NULL`; previously discarded values cannot be recovered.
Apply the schema upgrade through the normal approved release process before starting
the updated manager and API. All API instances must be updated before relying on
limit enforcement. Old cache rows are refreshed on first use, and edits invalidate
name, ID and default lookups. A binary rollback can retain the nullable column;
older API binaries do not enforce the limit.
