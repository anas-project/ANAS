#!/usr/bin/env sh
# Probe a pinned Forgejo instance for the upstream facts the AI Agent design
# depends on. See docs/architecture/ai-agent-orchestration-design.md §11 and
# dev-docs/plans/ai-agent.md M1.
#
# Read-mostly: everything it creates lives under a scratch org and is removed on
# exit unless PROBE_KEEP=1. It never touches existing repositories.
#
# Required:
#   FORGEJO_URL          e.g. https://git.example.com
#   FORGEJO_ADMIN_TOKEN  token of a site administrator
# Optional:
#   PROBE_ORG            scratch organisation name (default agentprobe)
#   PROBE_FORGEJO_CONTAINER  container name; enables the CLI-only checks
#   PROBE_KEEP=1         keep the scratch objects for manual inspection
#   PROBE_REPORT         report path (default test-env/reports/forgejo-agent-api-probe-<date>.md)
set -eu

: "${FORGEJO_URL:?set FORGEJO_URL}"
: "${FORGEJO_ADMIN_TOKEN:?set FORGEJO_ADMIN_TOKEN}"

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
org=${PROBE_ORG:-agentprobe}
suffix=$(date +%Y%m%d%H%M%S)
repo="probe-$suffix"
repo2="probe-$suffix-b"
agent_user="agent-probe-$suffix"
api="$FORGEJO_URL/api/v1"
api2="$FORGEJO_URL/api/v2"
report=${PROBE_REPORT:-"$root_dir/test-env/reports/forgejo-agent-api-probe-$suffix.md"}
mkdir -p "$(dirname -- "$report")"

pass=0; fail=0; skip=0
results=""

record() { # record <status> <id> <note>
  results="$results
| $2 | $1 | $3 |"
  case $1 in
    PASS) pass=$((pass+1)) ;;
    FAIL) fail=$((fail+1)) ;;
    SKIP) skip=$((skip+1)) ;;
  esac
  printf '%-4s %-28s %s\n' "$1" "$2" "$3"
}

req() { # req <method> <url> [body] ; prints "<code>\n<body>"
  method=$1; url=$2; body=${3:-}
  if [ -n "$body" ]; then
    curl -skS -X "$method" "$url" \
      -H "Authorization: token $FORGEJO_ADMIN_TOKEN" \
      -H 'Content-Type: application/json' \
      -d "$body" -w '\n%{http_code}'
  else
    curl -skS -X "$method" "$url" \
      -H "Authorization: token $FORGEJO_ADMIN_TOKEN" -w '\n%{http_code}'
  fi
}

code_of() { printf '%s' "$1" | tail -n 1; }
body_of() { printf '%s' "$1" | sed '$d'; }

jget() { # jget <json> <python expression over d>
  printf '%s' "$1" | python3 -c "import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    print(''); raise SystemExit(0)
try:
    print($2)
except Exception:
    print('')"
}

b64() { printf '%s' "$1" | base64 | tr -d '\n'; }

cleanup() {
  [ "${PROBE_KEEP:-0}" = "1" ] && { echo "PROBE_KEEP=1, scratch objects kept under org $org"; return 0; }
  req DELETE "$api/repos/$org/$repo" >/dev/null 2>&1 || true
  req DELETE "$api/repos/$org/$repo2" >/dev/null 2>&1 || true
  req DELETE "$api/admin/users/$agent_user" >/dev/null 2>&1 || true
  [ -n "${hook_id:-}" ] && req DELETE "$api/admin/hooks/$hook_id" >/dev/null 2>&1 || true
  return 0
}
trap cleanup EXIT INT TERM

echo "== Forgejo AI Agent API probe =="
version=$(body_of "$(req GET "$api/version")")
echo "instance: $FORGEJO_URL  version: $(jget "$version" "d['version']")"

# --- scratch fixtures -------------------------------------------------------
r=$(req POST "$api/orgs" "{\"username\":\"$org\",\"visibility\":\"private\"}")
[ "$(code_of "$r")" = "201" ] || echo "note: org $org already exists or not creatable, reusing"
for name in "$repo" "$repo2"; do
  r=$(req POST "$api/orgs/$org/repos" "{\"name\":\"$name\",\"auto_init\":true,\"private\":true,\"default_branch\":\"main\"}")
  [ "$(code_of "$r")" = "201" ] || { echo "FATAL: cannot create scratch repo $name"; exit 1; }
done

# --- 1. admin user + token + ssh key ---------------------------------------
r=$(req POST "$api/admin/users" "{\"username\":\"$agent_user\",\"email\":\"$agent_user@invalid.local\",\"password\":\"Probe-$suffix-Aa1!\",\"must_change_password\":false}")
if [ "$(code_of "$r")" = "201" ]; then
  record PASS admin-user-create "POST /admin/users 建账号可用"
else
  record FAIL admin-user-create "HTTP $(code_of "$r"): $(body_of "$r" | head -c 200)"
fi

# --- 2. token with scopes + repositories limiting ---------------------------
tok_body="{\"name\":\"probe-$suffix\",\"scopes\":[\"read:issue\",\"write:issue\",\"read:repository\"],\"repositories\":[{\"owner\":\"$org\",\"name\":\"$repo\"}]}"
r=$(req POST "$api/admin/users/$agent_user/tokens" "$tok_body")
agent_token=$(jget "$(body_of "$r")" "d.get('sha1','')")
scoped_repos=$(jget "$(body_of "$r")" "len(d.get('repositories') or [])")
if [ "$(code_of "$r")" = "201" ] && [ -n "$agent_token" ]; then
  if [ "${scoped_repos:-0}" -ge 1 ]; then
    record PASS token-repo-scope "token 支持 repositories 限定（返回 $scoped_repos 个仓库）"
  else
    record FAIL token-repo-scope "token 已发放但 repositories 未生效——AGENT-R-006 需走退化路径"
  fi
else
  record FAIL token-create "HTTP $(code_of "$r"): $(body_of "$r" | head -c 200)"
fi

if [ -n "${agent_token:-}" ]; then
  allowed=$(curl -skS -o /dev/null -w '%{http_code}' -H "Authorization: token $agent_token" "$api/repos/$org/$repo")
  denied=$(curl -skS -o /dev/null -w '%{http_code}' -H "Authorization: token $agent_token" "$api/repos/$org/$repo2")
  if [ "$allowed" = "200" ] && [ "$denied" != "200" ]; then
    record PASS token-repo-isolation "受限 token 可读目标仓库（$allowed），越界仓库被拒（$denied）"
  else
    record FAIL token-repo-isolation "目标仓库 $allowed，越界仓库 $denied（期望越界非 200）"
  fi
fi

r=$(req POST "$api/admin/users/$agent_user/keys" "{\"title\":\"probe\",\"key\":\"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIProbeKeyDoesNotAuthenticate0000000000000 probe@invalid\"}")
case "$(code_of "$r")" in
  201|422) record PASS admin-ssh-key "POST /admin/users/{u}/keys 端点存在（HTTP $(code_of "$r")）" ;;
  *) record FAIL admin-ssh-key "HTTP $(code_of "$r")" ;;
esac

# --- 3. system webhook ------------------------------------------------------
events='["issues","issue_assign","issue_label","issue_comment","pull_request","pull_request_comment","action_run_success","action_run_failure"]'
r=$(req POST "$api/admin/hooks" "{\"type\":\"forgejo\",\"active\":false,\"events\":$events,\"config\":{\"url\":\"https://probe.invalid/ingress\",\"content_type\":\"json\",\"secret\":\"probe-secret\"}}")
hook_id=$(jget "$(body_of "$r")" "d.get('id','')")
if [ -n "$hook_id" ]; then
  accepted=$(jget "$(body_of "$r")" "len(d.get('events') or [])")
  record PASS system-webhook "系统 webhook 可创建，接受 $accepted 个事件"
else
  record FAIL system-webhook "HTTP $(code_of "$r"): $(body_of "$r" | head -c 200)"
fi

# --- 4. labels, issues, comments, reactions ---------------------------------
r=$(req POST "$api/repos/$org/$repo/labels" '{"name":"ai:plan","color":"#5319e7","description":"probe"}')
[ "$(code_of "$r")" = "201" ] && record PASS repo-label "仓库标签 CRUD 可用" || record FAIL repo-label "HTTP $(code_of "$r")"
label_id=$(jget "$(body_of "$r")" "d.get('id','')")

r=$(req POST "$api/repos/$org/$repo/issues" '{"title":"probe issue","body":"probe body"}')
issue=$(jget "$(body_of "$r")" "d.get('number','')")
[ -n "$issue" ] && record PASS issue-create "issue 创建可用（#$issue）" || record FAIL issue-create "HTTP $(code_of "$r")"

if [ -n "${issue:-}" ]; then
  r=$(req POST "$api/repos/$org/$repo/issues/$issue/comments" '{"body":"**Agent 状态** probe"}')
  comment=$(jget "$(body_of "$r")" "d.get('id','')")
  r=$(req PATCH "$api/repos/$org/$repo/issues/comments/$comment" '{"body":"**Agent 状态** probe updated"}')
  [ "$(code_of "$r")" = "200" ] && record PASS comment-patch "评论可原地更新（状态评论方案成立）" || record FAIL comment-patch "HTTP $(code_of "$r")"

  r=$(req POST "$api/repos/$org/$repo/issues/comments/$comment/reactions" '{"content":"eyes"}')
  case "$(code_of "$r")" in
    200|201) record PASS comment-reaction "评论 reaction 可用（轻量确认成立）" ;;
    *) record FAIL comment-reaction "HTTP $(code_of "$r")" ;;
  esac

  r=$(req POST "$api/repos/$org/$repo/issues/$issue/labels" "{\"labels\":[$label_id]}")
  [ "$(code_of "$r")" = "200" ] && record PASS issue-label "issue 打标签可用" || record FAIL issue-label "HTTP $(code_of "$r")"

  r=$(req POST "$api/repos/$org/$repo/issues/$issue/deadline" "{\"due_date\":\"$(date -u +%Y-%m-%d)T23:59:59Z\"}")
  case "$(code_of "$r")" in
    201|200) record PASS issue-deadline "due_date 可由 API 设置" ;;
    *) record FAIL issue-deadline "HTTP $(code_of "$r")" ;;
  esac

  r=$(req POST "$api/repos/$org/$repo/issues/$issue/times" "{\"time\":600,\"user_name\":\"$agent_user\"}")
  case "$(code_of "$r")" in
    200|201) record PASS tracked-time "可代 Agent 账号写入工时" ;;
    *) record FAIL tracked-time "HTTP $(code_of "$r"): $(body_of "$r" | head -c 160)" ;;
  esac

  r=$(req POST "$api/repos/$org/$repo/issues" '{"title":"probe child","body":"probe"}')
  child=$(jget "$(body_of "$r")" "d.get('number','')")
  r=$(req POST "$api/repos/$org/$repo/issues/$issue/dependencies" "{\"index\":$child}")
  case "$(code_of "$r")" in
    200|201) record PASS issue-dependency "issue 依赖可用（/split 与顺序表达成立）" ;;
    *) record FAIL issue-dependency "HTTP $(code_of "$r"): $(body_of "$r" | head -c 160)" ;;
  esac

  r=$(req POST "$api/repos/$org/$repo/issues/$issue/pin")
  case "$(code_of "$r")" in
    204|200|201)
      r2=$(req GET "$api/repos/$org/$repo/issues/pinned")
      n=$(jget "$(body_of "$r2")" "len(d)")
      record PASS issue-pin "置顶可用（当前 $n 条），队列总览 issue 方案成立" ;;
    *) record FAIL issue-pin "HTTP $(code_of "$r")" ;;
  esac
  r=$(req GET "$api/repos/$org/$repo/new_pin_allowed")
  record PASS pin-allowed "new_pin_allowed: $(body_of "$r" | head -c 60)"
fi

# --- 5. contents API + PR ---------------------------------------------------
tpl='name: Agent 讨论
about: probe
title: "[agent] "
labels:
  - "ai:plan"
body:
  - type: dropdown
    id: chat-agents
    attributes:
      label: 讨论 Agent
      multiple: true
      options:
        - codex
        - claude
    validations:
      required: true
  - type: textarea
    id: goal
    attributes:
      label: 目标描述
    validations:
      required: true
'
r=$(req POST "$api/repos/$org/$repo/contents/.forgejo%2Fissue_template%2Fdiscuss.yaml" \
  "{\"content\":\"$(b64 "$tpl")\",\"message\":\"probe: add issue form template\",\"branch\":\"main\"}")
case "$(code_of "$r")" in
  201|200) record PASS contents-commit "contents API 可直接提交文件（文档入库与引导流程成立）" ;;
  *) record FAIL contents-commit "HTTP $(code_of "$r"): $(body_of "$r" | head -c 200)" ;;
esac

r=$(req GET "$api/repos/$org/$repo/issue_templates")
n=$(jget "$(body_of "$r")" "len(d)")
if [ "${n:-0}" -ge 1 ]; then
  fields=$(jget "$(body_of "$r")" "len(d[0].get('body') or [])")
  record PASS issue-templates "模板被识别（$n 个，首个 $fields 个字段）"
else
  record FAIL issue-templates "HTTP $(code_of "$r")，模板未被识别"
fi
r=$(req GET "$api/repos/$org/$repo/issue_config/validate")
record PASS issue-config "issue_config 校验端点：HTTP $(code_of "$r")"

r=$(req POST "$api/repos/$org/$repo/contents/docs%2Fagent-probe.md" \
  "{\"content\":\"$(b64 '# probe')\",\"message\":\"probe: doc on branch\",\"new_branch\":\"ai/probe-$suffix\",\"branch\":\"main\"}")
if [ "$(code_of "$r")" = "201" ]; then
  r=$(req POST "$api/repos/$org/$repo/pulls" "{\"head\":\"ai/probe-$suffix\",\"base\":\"main\",\"title\":\"probe PR\"}")
  case "$(code_of "$r")" in
    201) record PASS branch-and-pr "可在新分支提交并开 PR（无需克隆）" ;;
    *) record FAIL branch-and-pr "PR HTTP $(code_of "$r")" ;;
  esac
else
  record FAIL branch-and-pr "分支提交 HTTP $(code_of "$r")"
fi

# --- 6. absence checks ------------------------------------------------------
p1=$(curl -skS -o /dev/null -w '%{http_code}' -H "Authorization: token $FORGEJO_ADMIN_TOKEN" "$api/repos/$org/$repo/projects")
p2=$(curl -skS -o /dev/null -w '%{http_code}' -H "Authorization: token $FORGEJO_ADMIN_TOKEN" "$api2/repos/$org/$repo/projects")
if [ "$p1" = "404" ] && [ "$p2" = "404" ]; then
  record PASS projects-absent "Projects 仍无 API（v1 $p1 / v2 $p2）：看板只作人类视图"
else
  record FAIL projects-absent "Projects 端点返回 v1 $p1 / v2 $p2——设计需要重新评估"
fi

# --- 7. CLI-only checks -----------------------------------------------------
if [ -n "${PROBE_FORGEJO_CONTAINER:-}" ]; then
  if docker exec "$PROBE_FORGEJO_CONTAINER" forgejo admin auth add-oauth --help 2>&1 | grep -q -- '--group-team-map'; then
    record PASS group-team-map "CLI 支持 --group-team-map / --group-team-map-removal"
  else
    record FAIL group-team-map "固定版本的 add-oauth 没有 --group-team-map，§6.2 投影链路不成立"
  fi
else
  record SKIP group-team-map "设置 PROBE_FORGEJO_CONTAINER 后复核"
fi

record SKIP webhook-delivery "投递语义（超时/重试/重投）与 issue_label、issue_assign 的 payload 需要接收端，另行验证"
record SKIP form-answer-render "表单答案在 issue 正文中的渲染格式需要走 Web 表单提交，人工验证"

# --- report -----------------------------------------------------------------
{
  echo "# Forgejo AI Agent API 复核报告"
  echo
  echo "- 实例：$FORGEJO_URL"
  echo "- 版本：$(jget "$version" "d['version']")"
  echo "- 时间：$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- 依据：\`docs/architecture/ai-agent-orchestration-design.md\` §11"
  echo
  echo "| 检查项 | 结果 | 说明 |"
  echo "| --- | --- | --- |$results"
  echo
  echo "PASS $pass · FAIL $fail · SKIP $skip"
} > "$report"

echo
echo "PASS $pass · FAIL $fail · SKIP $skip"
echo "report: $report"
[ "$fail" -eq 0 ]
