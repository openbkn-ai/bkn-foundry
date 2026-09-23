import pytest

from app import evidence
from app.evidence_kafka import PublishResult


class AcceptedPublisher:
    def __init__(self):
        self.events = []

    def try_publish(self, event):
        self.events.append(event)
        return PublishResult("accepted", event_id=event["event_id"])


@pytest.fixture(autouse=True)
def accepted_evidence_publisher(monkeypatch):
    publisher = AcceptedPublisher()
    monkeypatch.setattr(evidence, "_publisher", publisher, raising=False)
    return publisher
