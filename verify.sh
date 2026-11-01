#!/usr/bin/env bash
# End-to-end check voor de self-hosted analytics MCP server.
# Gebruik: ./verify.sh            (lokaal + publiek)
#          ./verify.sh --local    (alleen localhost, geen DNS/nginx nodig)
set -uo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
LOCAL="http://127.0.0.1:8097"
PUBLIC="$(grep -E '^BASE_URL=' "$ROOT/.env" | cut -d= -f2-)"
PUBLIC="${PUBLIC%/}"
ONLY_LOCAL=0
[[ "${1:-}" == "--local" ]] && ONLY_LOCAL=1

pass=0; fail=0
ok()  { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass+1)); }
bad() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
code() { curl -s -o /dev/null -m 15 -w '%{http_code}' "$@"; }

echo "== container =="
docker compose -f "$ROOT/docker-compose.yml" ps --format '{{.Name}} {{.Status}}' | grep -q 'analytics-mcp' \
  && ok "container draait" || bad "container draait niet"

echo "== lokaal ($LOCAL) =="
c=$(code "$LOCAL/health"); [ "$c" = 200 ] && ok "GET /health -> 200" || bad "GET /health -> $c"
c=$(code -X POST "$LOCAL/" -H 'Content-Type: application/json' \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')
[ "$c" = 401 ] && ok "POST / zonder token -> 401" || bad "POST / zonder token -> $c"
c=$(code -X POST "$LOCAL/query" -H 'Content-Type: application/json' -d '{"connector":"searchconsole"}')
[ "$c" = 401 ] && ok "POST /query zonder token -> 401" || bad "POST /query zonder token -> $c"
c=$(code -X POST "$LOCAL/action" -H 'Content-Type: application/json' -d '{"connector":"google_ads"}')
[ "$c" = 401 ] && ok "POST /action zonder token -> 401" || bad "POST /action zonder token -> $c"

meta=$(curl -s -m 15 "$LOCAL/.well-known/oauth-authorization-server")
for scope in analytics.readonly webmasters.readonly adwords; do
  echo "$meta" | grep -q "$scope" && ok "scope $scope aangekondigd" || bad "scope $scope ontbreekt"
done

c=$(code -X POST "$LOCAL/register" -H 'Content-Type: application/json' \
     -d '{"redirect_uris":["http://127.0.0.1:8765/oauth/callback"],"client_name":"verify","token_endpoint_auth_method":"none"}')
[ "$c" = 201 ] && ok "DCR met loopback-redirect -> 201" || bad "DCR met loopback-redirect -> $c"

if [ "$ONLY_LOCAL" = 0 ]; then
  echo "== publiek ($PUBLIC) =="
  c=$(code "$PUBLIC/health"); [ "$c" = 200 ] && ok "GET /health -> 200" || bad "GET /health -> $c (Plesk/nginx nog niet doorverbonden?)"
  c=$(code -X POST "$PUBLIC/" -H 'Content-Type: application/json' \
       -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')
  [ "$c" = 401 ] && ok "POST / zonder token -> 401" || bad "POST / zonder token -> $c"
fi

echo
echo "geslaagd: $pass   mislukt: $fail"
[ "$fail" = 0 ]
