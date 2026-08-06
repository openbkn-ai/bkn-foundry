import json, os, pathlib

shards = json.loads(os.environ["SHARDS"])
root = pathlib.Path("findings")

done, missing, blockers, unconfirmed, lines = [], [], [], [], []
for s in shards:
    path = root / (s["key"] + ".json")
    if not path.exists():
        missing.append((s, "absent"))
        continue
    # 防御要罩住整个循环体，不能只罩 json.loads：文件解析成顶层数组时
    # data.get() 抛 AttributeError，同样会让 Assemble 非零退出。
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(data, dict):
            raise ValueError("not an object")
        # 回填的占位不算「产出过结论」：否则全片静默失败会变成 approve 一个
        # 从没被审过的 PR——比不表态糟得多。
        status = data.get("status")
    except (ValueError, OSError, AttributeError):
        missing.append((s, "malformed"))
        continue
    if status == "no_output":
        missing.append((s, "no_output"))
        continue
    done.append((s, data))
    # findings 是模型写的，属于不可信输入：JSON 合法但结构不对（元素是字符串
    # 而非对象）会让下面的 dict()/get() 抛异常，Assemble 非零退出后 Post review
    # 不执行、告警分支也不成立——PR 上既无表态也无告警，只剩一个红叉。
    # 序号取自 enumerate 而非计数器，跳过畸形元素不会让 id 错位（id 是
    # survived/refuted 分类的唯一依据，错位会把复核结论贴到别的阻塞项上）。
    # verify 的 collect 步骤必须用完全相同的过滤规则。
    for i, b in enumerate(data.get("blockers") or []):
        if not isinstance(b, dict):
            continue
        # id 规则必须与 verify 的 collect 步骤一致：<分片 key>#<片内序号>。
        b = dict(b, id="%s#%d" % (s["key"], i))
        blockers.append((s, b))
    for u in data.get("unconfirmed") or []:
        if isinstance(u, dict):
            unconfirmed.append((s, u))

# 一片都没产出（多半是 secret 缺失或全线失败）：不表态，只留告警。
# 此时 request-changes 会拿一个「什么都没审」当阻塞理由挂住 PR。
if not done:
    print("action=none")
    raise SystemExit(0)

# 复核结论：refuted 的阻塞项不进 PR，只留在折叠区里备查——全部丢掉的话，
# 复核环节把真问题误杀了也没人看得见。
verdicts = {}
vpath = pathlib.Path("verification/verified.json")
if vpath.exists():
    try:
        for v in json.loads(vpath.read_text(encoding="utf-8")).get("verdicts") or []:
            verdicts[v.get("id")] = v
    except (ValueError, OSError):
        verdicts = {}

# 所有面向 PR 的文字一律英文（PR 标题/正文/评审输出的既定口径）。此前这里
# 按分片投票在 zh/en 之间切换，结果是英文 PR 上仍会混进中文骨架与中文分片名。
T = {"block": "Blockers",
     "verified": "Each item below survived an independent verification pass (refuted by default; kept only when confirmed against the source).",
     "unrev": " (**unverified** — the verification stage produced no result)",
     "scen": "Failure scenario: ",
     "dropped": "%d item(s) dropped by verification", "why": "Refuted because: ",
     "byshard": "Per-shard findings", "impact": "Blast radius: ", "notcov": "Not covered: ",
     "clean": "_(no issues)_", "counts": "_(%d blocking, %d open)_",
     "detail": "Blast radius and coverage gaps, per shard",
     "more_unconf": "%d further open questions were trimmed to keep this readable; see the shard logs.",
     "unconf": "Open questions (non-blocking)", "missing": "Shards with no result",
     "missing_body": "These shards produced no findings — **their files were not reviewed this round**:",
     "reason_no_output": "ran, but wrote no conclusion (most likely the turn limit, or it wrapped up early)",
     "reason_absent": "never ran, or died partway through",
     "reason_malformed": "wrote a conclusion file that could not be parsed",
     "trunc": "File list truncated",
     "trunc_body": "This PR changes %s files but only %s reached the planner (`gh pr view --json files` caps at 100) — **the remaining %d were not assigned to any shard and were not reviewed**.",
     "footer": "Push a new commit when addressed, or reply `/review` for another round.",
     "footer_ok": "Disagree, or want another pass? Reply `/review` — a plain comment does not trigger one, and the replier must be an OWNER/MEMBER/COLLABORATOR of this repo."}

# 模型不一定听话，长度要有确定性兜底——与「模型输出是不可信输入」同一思路。
def clip(x, n):
    x = " ".join(str(x).split())
    return x if len(x) <= n else x[:n - 1].rstrip() + "…"

survived, refuted, unreviewed = [], [], []
for s_, b in blockers:
    v = verdicts.get(b["id"])
    if v is None:
        unreviewed.append((s_, b, None))
    elif v.get("refuted"):
        refuted.append((s_, b, v))
    else:
        survived.append((s_, b, v))

# 未复核的仍然当阻塞项报，但在正文里标出来——复核缺席是降可信度，不是免罪。
blockers = [(s_, b) for s_, b, _ in survived + unreviewed]

if blockers:
    lines.append("## %s\n" % T["block"])
    lines.append(T["verified"] + "\n")
    for s_, b in blockers:
        loc = b.get("file", "")
        if b.get("line"):
            loc += ":%s" % b["line"]
        mark = "" if verdicts.get(b["id"]) else T["unrev"]
        lines.append("- **[%s] %s** — %s%s"
                     % (s_["label"], loc, clip(b.get("what", ""), 300), mark))
        if b.get("scenario"):
            lines.append("  %s%s" % (T["scen"], clip(b["scenario"], 300)))
    lines.append("")

if refuted:
    lines.append("<details><summary>" + (T["dropped"] % len(refuted)) + "</summary>\n")
    for s_, b, v in refuted:
        lines.append("- **[%s] %s** — %s" % (s_["label"], b.get("file", ""),
                                             b.get("what", "")))
        lines.append("  %s%s" % (T["why"], v.get("reason", "")))
    lines.append("\n</details>\n")

# 标量字段同样是不可信输入：这里用 + 拼接（而非 %s 格式化）要求 str，
# 模型把 impact 写成数组就抛 TypeError，失败形态与上面挡掉的畸形元素一致
# ——Assemble 非零退出后既无表态也无告警。一律套 str()。
# 每片一行，详情折叠。改造前影响范围是整个 PR 写一次，分片后变成每片各写一段，
# 实测把结论正文撑到了改造前的 3 倍——阻塞项被埋在中间没人看得见。
lines.append("## %s\n" % T["byshard"])
for s, data in done:
    n_b = len([b for _, b in blockers if _ is s])
    n_u = len([u for ss, u in unconfirmed if ss is s])
    tag = T["clean"] if not (n_b or n_u) else T["counts"] % (n_b, n_u)
    lines.append("- **%s** — %s %s" % (s["label"], clip(data.get("summary") or "", 140), tag))
lines.append("")

detail = []
for s, data in done:
    extra = [(T["impact"], data.get("impact")), (T["notcov"], data.get("not_covered"))]
    extra = [(k, v) for k, v in extra if str(v or "").strip()]
    if not extra:
        continue
    detail.append("**%s**" % s["label"])
    detail += ["- %s%s" % (k, clip(v, 200)) for k, v in extra]
    detail.append("")
if detail:
    lines.append("<details><summary>%s</summary>\n" % T["detail"])
    lines += detail
    lines.append("</details>\n")

# 每片至多 3 条，且整体封顶——各片各写各的，不封顶就会线性膨胀。
if unconfirmed:
    lines.append("## %s\n" % T["unconf"])
    seen_per_shard, shown = {}, 0
    for s, u in unconfirmed:
        k = s["key"]
        if seen_per_shard.get(k, 0) >= 2 or shown >= 6:
            continue
        seen_per_shard[k] = seen_per_shard.get(k, 0) + 1
        shown += 1
        lines.append("- **[%s] %s** — %s"
                     % (s["label"], u.get("file", ""), clip(u.get("what", ""), 220)))
    dropped_u = len(unconfirmed) - shown
    if dropped_u > 0:
        lines.append("- _%s_" % (T["more_unconf"] % dropped_u))
    lines.append("")

if missing:
    lines.append("## %s\n" % T["missing"])
    lines.append(T["missing_body"] + "\n")
    # 两种原因指向完全不同的修法（轮次预算 vs job 失败），所以要分开写，
    # 不能都渲染成一行「未覆盖」。
    for s, reason in missing:
        lines.append("- %s — %s" % (s["label"], T["reason_" + reason]))
    lines.append("")

dropped = int(os.environ.get("FILE_TOTAL") or 0) - int(os.environ.get("FILE_LISTED") or 0)
if dropped > 0:
    lines.append("## %s\n" % T["trunc"])
    lines.append(T["trunc_body"] % (os.environ["FILE_TOTAL"],
                                os.environ["FILE_LISTED"], dropped) + "\n")

# approve 之后作者仍可能想争一句，而普通回帖不触发复评（评论正文必须含
# /review 或 @claude）。不写这句，他没有别的途径知道这件事。
lines.append(T["footer"] if blockers else T["footer_ok"])

pathlib.Path("verdict.md").write_text("\n".join(lines), encoding="utf-8")
print("action=" + ("request-changes" if blockers else "approve"))