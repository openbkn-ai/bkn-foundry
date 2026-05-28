#!/usr/bin/env bash
# verify-charts-vs-helm-repo.sh
#
# 验证仓库内的 helm chart 与 kweaver-ai/helm-repo 已发布的**最新版本** .tgz
# 是否结构等价：把"有意差异"（registry / repository / tag / version /
# _componentMeta.version / __VERSION__ 占位）归一化后做 diff，剩余差异就是
# 意外差异（templates 变更、values 业务字段变动等）。
#
# 用法：
#   bash scripts/verify-charts-vs-0.8.0.sh        # 跑全部，每 chart 自动用最新版
#   HELM_REPO=/path/to/clone bash …               # 用本地已 clone 的 helm-repo
#
# 退出码：0 = 全部 EQUIV / SKIP；1 = 至少 1 个 DIFF。

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
HELM_REPO="${HELM_REPO:-/tmp/helm-repo}"
WORK="${WORK:-/tmp/chart-verify}"
HELM_REPO_URL="https://github.com/kweaver-ai/helm-repo.git"

# 1) 确保 helm-repo 已 clone（不存在则浅克隆；已存在直接复用）
if [ ! -d "$HELM_REPO/.git" ]; then
  echo "Cloning $HELM_REPO_URL -> $HELM_REPO"
  rm -rf "$HELM_REPO"
  git clone --depth 1 "$HELM_REPO_URL" "$HELM_REPO" >/dev/null
fi

rm -rf "$WORK"; mkdir -p "$WORK"

# 归一化：把"有意差异"字段替换成稳定占位，让剩余 diff 只反映意外差异。
normalize() {
  local dir="$1"
  find "$dir" -type f \( -name 'Chart.yaml' -o -name 'values.yaml' -o -name '_componentMeta.json' \) | while read -r f; do
    case "$(basename "$f")" in
      Chart.yaml)
        # version + appVersion 任意值 -> 占位
        sed -i.bak -E 's/^version: .*/version: <V>/; s/^appVersion: .*/appVersion: "<V>"/' "$f" ;;
      values.yaml)
        # image.* 三件套各自归一（多镜像 chart 里出现多次也都替换）
        sed -i.bak -E '
          s|^([[:space:]]*registry:[[:space:]]*).*$|\1<R>|
          s|^([[:space:]]*repository:[[:space:]]*).*$|\1<REPO>|
          s|^([[:space:]]*tag:[[:space:]]*).*$|\1<T>|
        ' "$f" ;;
      _componentMeta.json)
        sed -i.bak -E 's|"version":[[:space:]]*"[^"]*"|"version": "<V>"|' "$f" ;;
    esac
    rm -f "$f.bak"
  done
}

# 2) 遍历仓库 Chart.yaml，按 chart name 找老 0.8.0.tgz，比对
EQUIV=0; DIFF=0; SKIP=0
declare -a DIFF_CHARTS=()

echo "===== Chart equivalence (repo vs helm-repo 0.8.0) ====="
while IFS= read -r chart_yaml; do
  chart_dir="$(dirname "$chart_yaml")"
  name=$(awk -F': ' '/^name:/{gsub(/[[:space:]]/,"",$2); print $2; exit}' "$chart_yaml")

  # 别名映射：仓库 chart 重命名后，老发布名仍要能匹配上
  # repo-name -> historical published name(s)
  case "$name" in
    data-migrator) lookup_names=("data-migrator" "kweaver-core-data-migrator") ;;
    *) lookup_names=("$name") ;;
  esac

  # 找 helm-repo/packages 里该 chart 的最新版本（按版本号自然排序，取最大）
  # 用 find 而非 ls：无匹配时 find 返 0，避免 set -e + pipefail 中断
  old_tgz=""
  for ln in "${lookup_names[@]}"; do
    cand=$(find "$HELM_REPO/packages" -maxdepth 1 -name "${ln}-[0-9]*.tgz" 2>/dev/null \
      | sort -V 2>/dev/null | tail -1)
    [ -z "$cand" ] && \
      cand=$(find "$HELM_REPO/packages" -maxdepth 1 -name "${ln}-[0-9]*.tgz" 2>/dev/null | sort | tail -1)
    if [ -n "$cand" ]; then old_tgz="$cand"; break; fi
  done

  if [ -z "$old_tgz" ] || [ ! -f "$old_tgz" ]; then
    printf "  SKIP   %-34s  (no published version in helm-repo)\n" "$name"
    SKIP=$((SKIP+1))
    continue
  fi
  old_ver=$(basename "$old_tgz" .tgz | sed "s|^${name}-||")

  old_dir="$WORK/old/$name"; new_dir="$WORK/new/$name"
  mkdir -p "$old_dir" "$new_dir"
  tar -xzf "$old_tgz" -C "$old_dir" --strip-components=1
  cp -R "$chart_dir/." "$new_dir/"

  normalize "$old_dir"
  normalize "$new_dir"

  diff_file="$WORK/$name.diff"
  # -B 忽略空行差异, -w 忽略空白/缩进差异 —— 这些是 repo 后期格式漂移，非业务变更
  if diff -ruN -B -w "$old_dir" "$new_dir" >"$diff_file" 2>&1; then
    printf "  \033[32mEQUIV\033[0m  %-34s  vs %-12s  (normalized: identical)\n" "$name" "$old_ver"
    EQUIV=$((EQUIV+1))
    rm -f "$diff_file"
  else
    n=$(grep -cE "^(diff |Only in)" "$diff_file" || true)
    printf "  \033[31mDIFF\033[0m   %-34s  vs %-12s  (%d differing files; %s)\n" "$name" "$old_ver" "$n" "$diff_file"
    DIFF=$((DIFF+1))
    DIFF_CHARTS+=("$name")
  fi
done < <(find "$REPO_ROOT" -name Chart.yaml -not -path '*/ref/*' -not -path '*/.git/*' | sort)

echo
echo "===== Summary ====="
printf "  EQUIV: %d  DIFF: %d  SKIP: %d\n" "$EQUIV" "$DIFF" "$SKIP"
if [ "$DIFF" -gt 0 ]; then
  echo
  echo "  Investigate diffs:"
  for c in "${DIFF_CHARTS[@]}"; do echo "    less $WORK/$c.diff"; done
  exit 1
fi
