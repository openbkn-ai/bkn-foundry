# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""The network's published object metrics, as the graph explorer's tools read them.

The analysis canvas of the EE explorer shows a metric as a card
(studio/src/graph-explorer/investigation/metric-card.ts). These helpers read a
metric the way that card does, so that a step of the Agent is the card a user
would make: a level groups by the time of a metric read by period, the
dimensions asked for, then one more for each drill; a drilled level is asked
for with the query of the level above and the key of the bucket picked in it,
looked up by the values that name the bucket. The model names buckets by their
values and periods by their names, never by key.
"""

from dataclasses import dataclass, field
from datetime import date, datetime, time as day_start, timedelta, timezone
from typing import Any, Awaitable, Callable, Literal

from langchain_core.tools import ToolException

from app.core.graph_explorer_data import text

# Periods are named, and dates read, in UTC+8.
TZ = timezone(timedelta(hours=8))
# Metrics the instructions list in full; past LIST_FULL they list names and ids, at most LIST_MAX.
LIST_FULL = 60
LIST_MAX = 300
COMMENT_CAP = 80
# Buckets a level reads (the card's METRIC_PAGE), and the pages looked through for a bucket.
ROWS = 200
FIND_PAGES = 10
# The rows the model reads, in characters.
ROW_CHARS = 12_000
# Participants read for a bucket at most (the card's PARTICIPANTS_MAX), and per request.
PARTICIPANTS_MAX = 1000
PARTICIPANT_PAGE = 100

# The views a metric's chart card can take (metric-charts.ts), but the change rate.
Chart = Literal["number", "bar", "pie", "stacked", "line"]
# One Context Loader tool call of the run: (tool name, **arguments) -> its JSON answer.
Call = Callable[..., Awaitable[dict]]


@dataclass
class Metric:
    id: str
    version: int
    name: str
    # The object type it counts.
    owner: str
    unit: str = ""
    comment: str = ""
    # The property refs it can be grouped by, and those it counts time by.
    dimensions: list[dict] = field(default_factory=list)
    time_dimensions: list[dict] = field(default_factory=list)


def _ref(raw: Any) -> dict | None:
    if not isinstance(raw, dict):
        return None
    ot, name = raw.get("object_type_id"), raw.get("property_name")
    if not (isinstance(ot, str) and ot and isinstance(name, str) and name):
        return None
    ref = {"object_type_id": ot, "property_name": name}
    alias = raw.get("relation_path_alias")
    if isinstance(alias, str) and alias:
        ref["relation_path_alias"] = alias
    return ref


def metric_from(raw: Any) -> Metric | None:
    """A related_metrics entry of get_object_types: a published metric, newest version."""
    if not isinstance(raw, dict):
        return None
    metric_id, version = raw.get("id"), raw.get("version")
    if not (isinstance(metric_id, str) and metric_id and isinstance(version, int) and version >= 1):
        return None
    return Metric(
        id=metric_id,
        version=version,
        name=text(raw.get("name")) or metric_id,
        owner=text(raw.get("owner_object_type_id")),
        unit=text(raw.get("unit")),
        comment=text(raw.get("comment")),
        dimensions=[r for r in map(_ref, raw.get("analysis_dimensions") or []) if r],
        time_dimensions=[r for r in map(_ref, raw.get("time_dimensions") or []) if r],
    )


def _key(ref: dict) -> tuple:
    return (ref["object_type_id"], ref["property_name"], ref.get("relation_path_alias") or "")


def label(metric: Metric, ref: dict) -> str:
    """A dimension as the model writes it: its property, led by its object type when that is
    not the one the metric counts, and the relation path it is reached along."""
    name = ref["property_name"]
    if ref["object_type_id"] != metric.owner:
        name = f"{ref['object_type_id']}.{name}"
    alias = ref.get("relation_path_alias")
    return f"{name}@{alias}" if alias else name


def _labels(metric: Metric, refs: list[dict]) -> str:
    return ", ".join(label(metric, r) for r in refs)


def ref_for(metric: Metric, written: str) -> dict:
    """The dimension the model wrote: as listed, or, when no other dimension has it, as
    `<object type>.<property>` or its property alone."""
    written = written.strip()
    dims = metric.dimensions
    for ref in dims:
        if label(metric, ref) == written:
            return ref
    for loose in (
        lambda ref: f"{ref['object_type_id']}.{ref['property_name']}",
        lambda ref: ref["property_name"],
    ):
        same = [ref for ref in dims if loose(ref) == written]
        if len(same) == 1:
            return same[0]
    raise ToolException(
        f"Metric {metric.name} cannot be split by {written!r}; the dimensions it opens are:"
        f" {_labels(metric, dims) or 'none'}. For another dimension work the figure out with"
        " run_cypher, and say that the metric does not open it."
    )


def _brief(value: str, cap: int) -> str:
    whole = " ".join(value.split())
    return whole if len(whole) <= cap else whole[: cap - 1] + "…"


def catalog(metrics: list[Metric], type_names: dict[str, str]) -> list[str]:
    """One line per metric for the instructions; past LIST_FULL, names and ids only."""
    full = len(metrics) <= LIST_FULL
    lines = []
    for metric in metrics[:LIST_MAX]:
        owner = f"{metric.owner} ({type_names[metric.owner]})" if type_names.get(metric.owner) else metric.owner
        line = f"- {metric.id} v{metric.version} {metric.name} — counts {owner}"
        if full:
            if metric.unit:
                line += f"; unit {metric.unit}"
            line += f"; split by: {_labels(metric, metric.dimensions) or 'none'}"
            if metric.time_dimensions:
                line += f"; time: {_labels(metric, metric.time_dimensions)}"
            if metric.comment.strip():
                line += f"; {_brief(metric.comment, COMMENT_CAP)}"
        lines.append(line)
    if not full:
        lines.append(f"({len(metrics)} metrics, so only their names are listed; query_metric says what a metric can be split by.)")
    if len(metrics) > LIST_MAX:
        lines.append(f"({len(metrics) - LIST_MAX} more are not listed.)")
    return lines


def day_range(start: str, end: str) -> dict:
    """Days, both ends in, as the service's range in milliseconds (UTC+8)."""
    try:
        first, last = date.fromisoformat(start.strip()), date.fromisoformat(end.strip())
    except ValueError:
        raise ToolException("time takes days as YYYY-MM-DD.") from None
    if last < first:
        raise ToolException("time.end is before time.start.")
    begin = datetime.combine(first, day_start(), TZ)
    after = datetime.combine(last + timedelta(days=1), day_start(), TZ)
    return {"start": int(begin.timestamp() * 1000), "end": int(after.timestamp() * 1000) - 1}


_DAY = 86_400_000
_GRAINS = (
    ("minute", 90_000),
    ("hour", 5_400_000),
    ("day", _DAY * 3 // 2),
    ("week", _DAY * 15 // 2),
    ("month", _DAY * 63 // 2),
    ("quarter", _DAY * 185 // 2),
)


def _bucket(entry: dict) -> dict | None:
    bucket = entry.get("time_bucket")
    if isinstance(bucket, dict) and isinstance(bucket.get("start"), int) and isinstance(bucket.get("end"), int):
        return bucket
    return None


def _mid(entry: dict) -> int | None:
    """A period by its middle, as the card keeps it: the middle of a day or longer falls
    in the same day, month or year read in any zone."""
    bucket = _bucket(entry)
    if bucket:
        return (bucket["start"] + bucket["end"]) // 2
    at = entry.get("time")
    return at if isinstance(at, int) else None


def period(entry: dict) -> str | None:
    """A bucket's period by its name; the grain is read off the bucket's length."""
    mid = _mid(entry)
    if mid is None:
        return None
    bucket = _bucket(entry)
    length = bucket["end"] - bucket["start"] + 1 if bucket else _DAY
    return period_name(mid, next((name for name, most in _GRAINS if length <= most), "year"))


def period_name(mid: int, grain: str) -> str:
    """A period's name as the card shows it (periodText), from its middle: 2026, 2026 Q1,
    2026-03, 2026-03-05, 2026-03-05 08:00; a week by its middle day."""
    at = datetime.fromtimestamp(mid / 1000, TZ)
    day = f"{at.year}-{at.month:02d}-{at.day:02d}"
    if grain == "year":
        return str(at.year)
    if grain == "quarter":
        return f"{at.year} Q{(at.month - 1) // 3 + 1}"
    if grain == "month":
        return f"{at.year}-{at.month:02d}"
    if grain == "hour":
        return f"{day} {at.hour:02d}:00"
    if grain == "minute":
        return f"{day} {at.hour:02d}:{at.minute:02d}"
    return day


def _cell(entry: dict, ref: dict) -> Any:
    for item in entry.get("dimension_cells") or []:
        found = _ref(item.get("ref")) if isinstance(item, dict) else None
        if found and _key(found) == _key(ref):
            return item.get("value")
    return None


def shown(value: Any) -> str:
    """A bucket's value as the model reads and writes it."""
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return text(value).strip()


def _cells(entry: dict, dims: list[dict], time_series: bool) -> list:
    """The values naming a bucket, as the card keeps them: the period's middle first."""
    return ([_mid(entry)] if time_series else []) + [_cell(entry, d) for d in dims]


def _time_series(answer: dict) -> bool:
    model = answer.get("model")
    return isinstance(model, dict) and model.get("output_shape") == "time_series"


def level_query(dims: list[dict], time_series: bool, window: dict | None) -> dict:
    """A level's query as the card asks it (levelQuery): latest periods first, then the
    largest values, at most ROWS buckets."""
    order = ([{"field": "time", "direction": "desc"}] if time_series else []) + (
        [{"field": "value", "direction": "desc"}] if dims else []
    )
    query: dict[str, Any] = {}
    if dims:
        query["group_by"] = [dict(d) for d in dims]
    if order:
        query["order_by"] = order
    if window:
        query["time"] = dict(window)
    query["page_size"] = ROWS
    return query


@dataclass
class Reading:
    """A metric read down to the level the model asked for."""

    metric: Metric
    time_series: bool
    group_by: list[dict]
    # Each drilled bucket by its values, and the dimension it was split by, as the card keeps them.
    drill: list[dict]
    window: dict | None
    # The level's query, its first page, and the dimensions it groups by (the time aside).
    query: dict
    answer: dict
    dims: list[dict]
    # The values and period picked on the way down, for a bucket of this level to leave out.
    picked: dict
    period: str | None


def _describe(metric: Metric, entry: dict, dims: list[dict], time_series: bool) -> str:
    parts = ([f"period {period(entry)}"] if time_series else []) + [
        f"{label(metric, d)}={shown(_cell(entry, d))}" for d in dims
    ]
    return ", ".join(parts) or "the total"


def _wanted(metric: Metric, dims: list[dict], picked: dict, bucket: dict, where: str) -> dict:
    wanted = dict(picked)
    for written, value in (bucket or {}).items():
        ref = ref_for(metric, written)
        if not any(_key(ref) == _key(d) for d in dims):
            raise ToolException(
                f"{where}: the level is not split by {written}; it is split by"
                f" {_labels(metric, dims) or 'nothing (it is the total)'}."
            )
        wanted[_key(ref)] = shown(value)
    missing = [d for d in dims if _key(d) not in wanted]
    if missing:
        raise ToolException(f"{where}: say which {_labels(metric, missing)} the bucket is.")
    return wanted


def _period_of(metric: Metric, written: str | None, picked: str | None, where: str) -> str:
    name = (written or "").strip() or picked
    if not name:
        raise ToolException(
            f"{where}: {metric.name} is read by period, so name the bucket's period as the rows do (e.g. 2026-03)."
        )
    return name


def _matches(entry: dict, dims: list[dict], wanted: dict, period_name: str | None) -> bool:
    if period_name is not None and (period(entry) or "").casefold() != period_name.casefold():
        return False
    return all(shown(_cell(entry, d)) == wanted[_key(d)] for d in dims)


async def _find(ask, query: dict, answer: dict, dims, wanted, period_name, describe) -> dict:
    """The bucket of a level named by its values, looked through page by page."""
    page = answer
    for turn in range(FIND_PAGES):
        for entry in page.get("entries") or []:
            if isinstance(entry, dict) and _matches(entry, dims, wanted, period_name):
                return entry
        cursor = page.get("next_cursor")
        if not cursor or turn == FIND_PAGES - 1:
            break
        page = await ask(query, cursor)
    some = [describe(e) for e in (answer.get("entries") or [])[:10] if isinstance(e, dict)]
    raise ToolException(
        "No such bucket. "
        + (f"Its level has: {'; '.join(some)}." if some else "Its level has no buckets.")
        + " Name a bucket by the values the rows show."
    )


async def read(
    call: Call,
    metric: Metric,
    group_by: list[str],
    drill: list[dict],
    window: dict | None,
    shapes: dict,
) -> Reading:
    """Reads the metric down the drill. `shapes` remembers, for the run, which metric
    versions are read by period: the first level is asked again for one that is, as
    only those are ordered by time."""
    refs: list[dict] = []
    for written in group_by:
        ref = ref_for(metric, written)
        if any(_key(ref) == _key(r) for r in refs):
            raise ToolException(f"{written} is named twice in group_by.")
        refs.append(ref)

    async def ask(query: dict, cursor: str | None = None) -> dict:
        return await call(
            "query_metric",
            metric_id=metric.id,
            version=metric.version,
            query={**query, "cursor": cursor} if cursor else query,
        )

    shape = (metric.id, metric.version)
    time_series = shapes.get(shape)
    query = level_query(refs, bool(time_series), window)
    answer = await ask(query)
    if time_series is None:
        time_series = shapes[shape] = _time_series(answer)
        if time_series:
            query = level_query(refs, True, window)
            answer = await ask(query)

    dims = list(refs)
    picked: dict = {}
    period_name: str | None = None
    steps: list[dict] = []
    for index, step in enumerate(drill):
        where = f"drill step {index + 1}"
        by = ref_for(metric, step.get("by") or "")
        if any(_key(by) == _key(d) for d in dims):
            free = [d for d in metric.dimensions if not any(_key(d) == _key(u) for u in dims)]
            raise ToolException(
                f"{where}: the level is already split by {label(metric, by)}; it can still be split by"
                f" {_labels(metric, free) or 'nothing'}."
            )
        wanted = _wanted(metric, dims, picked, step.get("bucket") or {}, where)
        if time_series:
            period_name = _period_of(metric, step.get("period"), period_name, where)
        entry = await _find(
            ask, query, answer, dims, wanted, period_name if time_series else None,
            lambda e, dims=dims: _describe(metric, e, dims, bool(time_series)),
        )
        if "drill" not in (entry.get("available_actions") or []) or not entry.get("bucket_key"):
            raise ToolException(f"{where}: the bucket {_describe(metric, entry, dims, time_series)} cannot be drilled into.")
        steps.append({"cells": _cells(entry, dims, time_series), "by": dict(by)})
        picked = {k: v for k, v in wanted.items() if any(_key(d) == k for d in dims)}
        query = {
            **level_query([*dims, by], time_series, window),
            "bucket_selection": {"parent_query": query, "bucket_key": entry["bucket_key"]},
        }
        dims = [*dims, by]
        answer = await ask(query)
    return Reading(
        metric=metric,
        time_series=time_series,
        group_by=[dict(r) for r in refs],
        drill=steps,
        window=window,
        query=query,
        answer=answer,
        dims=dims,
        picked=picked,
        period=period_name,
    )


def views(reading: Reading) -> list[str]:
    """The charts that can show the level (viewsFor of metric-charts.ts)."""
    drilled = bool(reading.drill)
    if reading.time_series and not drilled:
        return ["line", "stacked", "bar"] if reading.group_by else ["line", "bar"]
    varying = 1 if drilled else len(reading.group_by) + (1 if reading.time_series else 0)
    if varying == 0:
        return ["number"]
    return ["bar", "pie", "stacked"] if varying >= 2 else ["bar", "pie"]


def answer(reading: Reading) -> dict:
    """What the model reads of a level: one row per bucket, earliest period first."""
    metric, data = reading.metric, reading.answer
    entries = [e for e in data.get("entries") or [] if isinstance(e, dict)]
    if reading.time_series:
        entries.sort(key=lambda e: _mid(e) or 0)
    rows, size = [], 0
    for entry in entries:
        row: dict[str, Any] = {}
        if reading.time_series:
            row["period"] = period(entry)
        for d in reading.dims:
            row[label(metric, d)] = shown(_cell(entry, d))
        value = entry.get("display_value")
        row["value"] = value if isinstance(value, str) and value else (
            None if entry.get("value") is None else shown(entry.get("value"))
        )
        if entry.get("status") not in (None, "ok"):
            row["status"] = entry["status"]
        actions = entry.get("available_actions") or []
        if "drill" in actions:
            row["drill"] = True
        if "participants" in actions:
            row["participants"] = True
        size += len(str(row))
        if rows and size > ROW_CHARS:
            break
        rows.append(row)
    model = data.get("model") if isinstance(data.get("model"), dict) else {}
    unit = metric.unit or text(((model.get("value") or {}).get("unit") or {}).get("code"))
    free = [d for d in metric.dimensions if not any(_key(d) == _key(u) for u in reading.dims)]
    out: dict[str, Any] = {"metric": metric.name, "id": metric.id, "version": metric.version}
    if unit:
        out["unit"] = unit
    out["split_by"] = (["period"] if reading.time_series else []) + [label(metric, d) for d in reading.dims]
    out["rows"] = rows
    total = data.get("total_count")
    if len(rows) < len(entries) or data.get("next_cursor"):
        out["cut"] = (
            f"Only {len(rows)} of {total if isinstance(total, int) and total else 'more'} rows are shown."
            " Drill into a bucket or split by fewer dimensions for the rest."
        )
    if data.get("status") not in (None, "ok"):
        out["status"] = data["status"]
    if data.get("is_approximate"):
        out["approximate"] = True
    if data.get("warnings"):
        out["warnings"] = data["warnings"]
    out["can_split_further_by"] = [label(metric, d) for d in free]
    return out


def spec(reading: Reading) -> dict:
    """The level as the card keeps it, for the page to make that card."""
    out: dict[str, Any] = {
        "metric_id": reading.metric.id,
        "version": reading.metric.version,
        "owner_object_type_id": reading.metric.owner,
        "group_by": reading.group_by,
    }
    if reading.drill:
        out["drill"] = reading.drill
    if reading.window:
        out["time"] = reading.window
    return out


def _day(at: int) -> str:
    return datetime.fromtimestamp(at / 1000, TZ).date().isoformat()


def card_query(card: dict) -> dict | None:
    """What a metric card reads (as spec gives it, with the metric's grain when it is read
    by period) in query_metric's arguments, for a later question to go on from; None when
    the card's query does not hold together."""
    metric = Metric(id=card["metric_id"], version=card["version"], name="", owner=card.get("owner_object_type_id") or "")
    dims = [_ref(raw) for raw in card.get("group_by") or []]
    if not all(dims):
        return None
    out: dict[str, Any] = {"metric": metric.id, "version": metric.version}
    if dims:
        out["group_by"] = [label(metric, d) for d in dims]
    grain = card.get("time_grain") or ""
    steps = []
    for step in card.get("drill") or []:
        by = _ref(step.get("by")) if isinstance(step, dict) else None
        cells = step.get("cells") if by else None
        if not isinstance(cells, list):
            return None
        # A metric read by period names its buckets by the period's middle first.
        timed = len(cells) == len(dims) + 1
        if not (timed or len(cells) == len(dims)) or (timed and not isinstance(cells[0], int)):
            return None
        written: dict[str, Any] = {}
        if timed:
            written["period"] = period_name(cells[0], grain or "day")
        bucket = {label(metric, d): shown(v) for d, v in zip(dims, cells[1:] if timed else cells)}
        if bucket:
            written["bucket"] = bucket
        written["by"] = label(metric, by)
        steps.append(written)
        dims = [*dims, by]
    if steps:
        out["drill"] = steps
    window = card.get("time")
    if isinstance(window, dict) and isinstance(window.get("start"), int) and isinstance(window.get("end"), int):
        out["time"] = {"start": _day(window["start"]), "end": _day(window["end"])}
    return out


async def participants(
    call: Call, reading: Reading, bucket: dict, period_name: str | None
) -> tuple[list, list[dict], bool, list[str]]:
    """One bucket of the level: the values naming it as the card keeps them, the
    participants read (at most PARTICIPANTS_MAX), whether there are more, and warnings."""
    metric, dims = reading.metric, reading.dims
    where = "bucket"
    wanted = _wanted(metric, dims, reading.picked, bucket, where)
    name = _period_of(metric, period_name, reading.period, where) if reading.time_series else None

    async def ask(query: dict, cursor: str | None = None) -> dict:
        return await call(
            "query_metric", metric_id=metric.id, version=metric.version,
            query={**query, "cursor": cursor} if cursor else query,
        )

    entry = await _find(
        ask, reading.query, reading.answer, dims, wanted, name,
        lambda e: _describe(metric, e, dims, reading.time_series),
    )
    if "participants" not in (entry.get("available_actions") or []) or not entry.get("bucket_key"):
        raise ToolException(
            f"The instances behind {_describe(metric, entry, dims, reading.time_series)} cannot be listed for this metric."
        )
    found: list[dict] = []
    warnings: list[str] = []
    cursor = None
    while True:
        page = await call(
            "drill_through_metric",
            metric_id=metric.id,
            version=metric.version,
            query=reading.query,
            bucket_key=entry["bucket_key"],
            page_size=PARTICIPANT_PAGE,
            **({"cursor": cursor} if cursor else {}),
        )
        found.extend(p for p in page.get("participants") or [] if isinstance(p, dict))
        warnings.extend(w for w in page.get("warnings") or [] if isinstance(w, str) and w not in warnings)
        cursor = page.get("next_cursor")
        if not cursor or len(found) >= PARTICIPANTS_MAX:
            break
    more = bool(cursor) or len(found) > PARTICIPANTS_MAX
    return _cells(entry, dims, reading.time_series), found[:PARTICIPANTS_MAX], more, warnings
