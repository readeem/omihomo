#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
mkdir "$test_root/bin"
export SOCKET_TEST_ROOT="$test_root"

cat >"$test_root/bin/ip" <<'EOF'
#!/usr/bin/env bash
printf '[{"addr_info":[{"local":"192.168.42.10"},{"local":"2001:db8::10"}]}]\n'
EOF
cat >"$test_root/bin/ss" <<'EOF'
#!/usr/bin/env bash
[[ ${SOCKET_TEST_FAIL:-no} != yes ]] || exit 1
cat "$SOCKET_TEST_ROOT/ss.txt"
EOF
chmod +x "$test_root/bin/"*

cat >"$test_root/ss.txt" <<EOF
udp UNCONN 0 0 0.0.0.0:53000 0.0.0.0:* users:(("firefox",pid=$$,fd=4))
tcp ESTAB 0 0 [2001:db8::10]:54000 [2001:db8::20]:443 users:(("browser",pid=$$,fd=5))
udp UNCONN 0 0 0.0.0.0:55000 0.0.0.0:*
udp UNCONN 0 0 0.0.0.0:56000 0.0.0.0:* users:(("first",pid=$$,fd=6),("second",pid=$$,fd=7))
udp UNCONN 0 0 0.0.0.0:57000 0.0.0.0:* users:(("gone",pid=2147483647,fd=8))
EOF

output=$(PATH="$test_root/bin:$PATH" "$project_root/bin/omihomo" socket-processes)
jq -e '
  .addresses == ["192.168.42.10", "2001:db8::10"] and
  (.sockets | length) == 5 and
  .sockets[0].process == "firefox" and .sockets[0].peerPort == 0 and
  .sockets[1].localIP == "2001:db8::10" and .sockets[1].peerIP == "2001:db8::20" and
  .sockets[2].process == "" and .sockets[3].process == "" and .sockets[4].process == ""
' <<<"$output" >/dev/null

printf '%s' "$output" | node -e '
const Model = require(process.argv[1]), assert = require("node:assert/strict");
const table = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const item = Model.parseConnections(JSON.stringify({connections:[{id:"example", metadata:{
  type:"Tun", network:"udp", sourceIP:"192.168.42.10", sourcePort:"53000",
  destinationIP:"203.0.113.5", destinationPort:"443"
}}]})).items[0];
assert.equal(Model.enrichConnections([item], table)[0].process, "firefox");
' "$project_root/Model.js"

printf 'udp UNCONN 0 0 0.0.0.0:53000 0.0.0.0:*\n' >>"$test_root/ss.txt"
output=$(PATH="$test_root/bin:$PATH" "$project_root/bin/omihomo" socket-processes)
printf '%s' "$output" | node -e '
const Model = require(process.argv[1]), assert = require("node:assert/strict");
const table = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const item = Model.parseConnections(JSON.stringify({connections:[{id:"example", metadata:{
  type:"Tun", network:"udp", sourceIP:"192.168.42.10", sourcePort:"53000"
}}]})).items[0];
assert.equal(Model.enrichConnections([item], table)[0].process, "");
' "$project_root/Model.js"

output=$(SOCKET_TEST_FAIL=yes PATH="$test_root/bin:$PATH" "$project_root/bin/omihomo" socket-processes)
jq -e '.addresses == [] and .sockets == []' <<<"$output" >/dev/null
printf 'PASS socket process lookup\n'
