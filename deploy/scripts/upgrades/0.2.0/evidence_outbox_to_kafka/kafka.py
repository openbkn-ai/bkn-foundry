"""Thin Kafka producer ACK adapter for already encoded migration Records."""

from manifest import ManifestError


def publish_with_ack(producer, topic, record, timeout_seconds):
    """Send one encoded Record and return its durable broker coordinate.

    The concrete client is injected by the maintenance overlay. It must expose
    `send(topic, key=..., value=..., headers=...)` returning a future with
    `get(timeout=...)`; no credentials or central-DB capability live here.
    """
    if not isinstance(topic, str) or not topic or not isinstance(timeout_seconds, (int, float)) or timeout_seconds <= 0:
        raise ManifestError("Kafka publish configuration is invalid")
    try:
        headers = [(key, value.encode("utf-8")) for key, value in record["headers"].items()]
        future = producer.send(topic, key=record["key"].encode("utf-8"), value=record["value"], headers=headers)
        metadata = future.get(timeout=timeout_seconds)
        coordinate = {"topic": metadata.topic, "partition": metadata.partition, "offset": metadata.offset}
    except (AttributeError, KeyError, TypeError, UnicodeError) as error:
        raise ManifestError("Kafka producer does not satisfy migration ACK contract") from error
    if coordinate["topic"] != topic or not isinstance(coordinate["partition"], int) or coordinate["partition"] < 0 or not isinstance(coordinate["offset"], int) or coordinate["offset"] < 0:
        raise ManifestError("Kafka producer returned an invalid ACK coordinate")
    return coordinate
