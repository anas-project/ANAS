#!/bin/sh

set -eu

catalog=/usr/share/lemonldap-ng/portal/htdocs/static/languages/zh.json
[ -f "$catalog" ] || exit 0

min_length=$SAMBA_DC_USER_MIN_PASS_LENGTH
history=$SAMBA_DC_USER_PASSWORD_HISTORY

policy="新密码至少 ${min_length} 个字符"
if [ "$SAMBA_DC_USER_COMPLEX_PASS" = "true" ]; then
  policy="${policy}；至少包含以下四类中的三类：大写字母、小写字母、数字、符号；不能包含用户名或姓名"
fi
if [ "$history" -gt 0 ]; then
  policy="${policy}；不能重复最近 ${history} 个密码"
fi
policy="${policy}。请在两个输入框中输入完全相同的新密码。"

tmp=$(mktemp)
jq \
  --arg min_length "$min_length" \
  --arg history "$history" \
  --arg policy "$policy" \
  '
    .PE25 = "这是临时密码，登录前必须设置新密码。请按照下方规则填写。" |
    .PE26 = "当前不允许修改密码。请确认新密码符合下方规则；如果刚修改过密码，请稍后再试或联系管理员。" |
    .PE28 = "新密码不符合域密码策略。请按下方规则重新设置，并避免使用用户名、姓名或最近使用过的密码。" |
    .PE29 = ("新密码太短，至少需要 " + $min_length + " 个字符。") |
    .PE30 = "新密码复杂度不足。请至少使用大写字母、小写字母、数字、符号四类中的三类。" |
    .PE31 = (if ($history | tonumber) > 0 then "这个密码最近使用过。请不要重复最近 " + $history + " 个密码。" else "这个密码最近使用过，请换一个新密码。" end) |
    .PE34 = "两次输入的新密码不一致，请重新输入。" |
    .passwordPolicy = $policy |
    .passwordPolicyMinSize = "最少字符数：" |
    .passwordPolicySamePwd = "两次输入的新密码必须完全一致。"
  ' "$catalog" >"$tmp"
cat "$tmp" >"$catalog"
rm -f "$tmp"
