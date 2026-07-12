# In-cluster live merge for the install-status snapshot (runs in the refresher
# via jq — the kubectl-shell image ships jq but no python).
#
# Inputs (via --slurpfile):
#   $snap — publish-time snapshot /data/install-status.json (expected releases)
#   $work — refresher dump /live/workloads.json (actual deploy/sts state)
# Arg:
#   $now  — UTC ISO timestamp
#
# Output: the snapshot with per-release appVersion overlaid from the actual
# running image tags (versionSource=live), readiness refreshed, and
# generatedAt/liveMergedAt set to $now. Any error aborts jq; the wrapper keeps
# the previous live file, so failures degrade to pre-existing behaviour.

($snap[0]) as $s
| ($work[0]) as $w
| (reduce ($w.items // [])[] as $it ({};
    ($it.metadata // {}) as $md
    | ((($md.annotations // {})["meta.helm.sh/release-name"])
        // (($md.labels // {})["app.kubernetes.io/instance"])
        // $md.name) as $rel
    | if $rel == null then .
      else
        (.[$rel] // {tags: [], ready: 0, total: 0}) as $e
        | ([($it.spec.template.spec.containers // [])[]
            | .image
            | split("/") | last
            | if contains(":") then (split(":") | last) else "latest" end
           ]) as $tags
        | .[$rel] = {
            tags:  (($e.tags + $tags) | unique),
            ready: ($e.ready + ($it.status.readyReplicas // 0)),
            total: ($e.total + ($it.spec.replicas // 1))
          }
      end)) as $actual
| $s
| .releases |= ((. // []) | map(
    . as $r
    | ($actual[$r.name] // null) as $m
    | if $m == null then .
      else
        . + {ready: "\($m.ready)/\($m.total)"}
          + (if ($m.tags | length) > 0
             then {appVersion: ($m.tags | join(",")), versionSource: "live"}
             else {} end)
      end))
| .generatedAt = $now
| .liveMergedAt = $now
