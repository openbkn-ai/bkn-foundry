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
