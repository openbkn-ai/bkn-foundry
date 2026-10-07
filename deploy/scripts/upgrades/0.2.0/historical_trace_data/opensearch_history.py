"""Read-before-create and readback for native historical Evidence aggregates."""
import base64
import json
import ssl
from datetime import timezone
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlparse
from urllib.request import HTTPSHandler, Request, ProxyHandler, build_opener


def _iso(value):
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).isoformat(timespec="microseconds" if value.microsecond else "seconds").replace("+00:00", "Z")


class OpenSearchHistoryWriter:
    def __init__(self, endpoint, index, username=None, password=None, opener=None, timeout=15, verify_tls=True, ca_file=None):
        parsed = urlparse(endpoint)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.username or parsed.password:
            raise ValueError("invalid OpenSearch endpoint")
        if not index or any(char in index for char in " /#?"):
            raise ValueError("invalid OpenSearch log index")
        self.endpoint = endpoint.rstrip("/")
        self.index = index
        self.username = username
        self.password = password
        if opener is not None:
            self.opener = opener
        elif parsed.scheme == "https" and not verify_tls:
            self.opener = build_opener(ProxyHandler({}), HTTPSHandler(context=ssl._create_unverified_context())).open
        elif parsed.scheme == "https":
            self.opener = build_opener(ProxyHandler({}), HTTPSHandler(context=ssl.create_default_context(cafile=ca_file))).open
        else:
            self.opener = build_opener(ProxyHandler({})).open
        self.timeout = timeout

    def _request(self, method, path, body=None, content_type="application/json"):
        headers = {"Content-Type": content_type}
        if self.username is not None or self.password is not None:
            if not self.username or not self.password:
                raise ValueError("OpenSearch credentials are incomplete")
            token = base64.b64encode((self.username + ":" + self.password).encode()).decode()
            headers["Authorization"] = "Basic " + token
        request = Request(self.endpoint + path, data=body, headers=headers, method=method)
        try:
            with self.opener(request, timeout=self.timeout) as response:
                return response.status, response.read()
        except HTTPError as error:
            return error.code, error.read()
        except (URLError, OSError) as error:
            raise RuntimeError("OpenSearch request failed") from error

    @staticmethod
    def _matches(existing, candidate):
        if not isinstance(existing, dict):
            return False
        comparable = dict(candidate)
        if existing.get("ingested_at"):
            comparable["ingested_at"] = existing["ingested_at"]
        return existing == comparable

    def publish_documents(self, items, observed_at=None, *, allow_update=False, on_result=None):
        counts = {"created": 0, "updated": 0, "already_verified": 0, "conflict": 0}
        for start in range(0, len(items), 200):
            chunk = items[start:start + 200]
            status, body = self._request("POST", "/" + quote(self.index, safe="") + "/_mget",
                                         json.dumps({"docs": [{"_id": item["_id"]} for item in chunk]}, separators=(",", ":")).encode())
            if status != 200:
                raise RuntimeError("OpenSearch history readback failed")
            existing_docs = json.loads(body).get("docs", [])
            if len(existing_docs) != len(chunk):
                raise RuntimeError("OpenSearch history readback count mismatch")
            lines = []
            actions = []
            for item, existing_doc in zip(chunk, existing_docs):
                if existing_doc.get("found") and self._matches(existing_doc.get("_source"), item["document"]):
                    actions.append("already")
                    continue
                if existing_doc.get("found") and not allow_update:
                    actions.append("conflict")
                    continue
                action = "index" if existing_doc.get("found") else "create"
                actions.append(action)
                lines.append(json.dumps({action: {"_index": self.index, "_id": item["_id"]}}, separators=(",", ":")))
                lines.append(json.dumps(item["document"], ensure_ascii=False, separators=(",", ":")))
            if lines:
                status, body = self._request("POST", "/_bulk", ("\n".join(lines) + "\n").encode(),
                                             content_type="application/x-ndjson")
                if status != 200:
                    raise RuntimeError("OpenSearch bulk history write failed")
                response = json.loads(body)
                results = response.get("items", [])
                if len(results) != sum(action in ("create", "index") for action in actions):
                    raise RuntimeError("OpenSearch bulk response count mismatch")
                failed = set()
                result_index = 0
                for item_index, action in enumerate(actions):
                    if action in ("already", "conflict"):
                        continue
                    result = results[result_index].get(action, {}) if result_index < len(results) else {}
                    result_index += 1
                    if result.get("status") not in {200, 201}:
                        failed.add(item_index)
            else:
                failed = set()
            status, body = self._request("POST", "/" + quote(self.index, safe="") + "/_mget",
                                         json.dumps({"docs": [{"_id": item["_id"]} for item in chunk]}, separators=(",", ":")).encode())
            if status != 200:
                raise RuntimeError("OpenSearch history readback failed")
            docs = json.loads(body).get("docs", [])
            if len(docs) != len(chunk):
                raise RuntimeError("OpenSearch history readback count mismatch")
            for item_index, (item, doc, action) in enumerate(zip(chunk, docs, actions)):
                if action == "conflict" or item_index in failed or not doc.get("found") or not self._matches(doc.get("_source"), item["document"]):
                    state = "conflict"
                else:
                    state = {"already": "already_verified", "create": "created", "index": "updated"}[action]
                counts[state] += 1
                if on_result is not None:
                    on_result(item["_id"], state)
        return counts
