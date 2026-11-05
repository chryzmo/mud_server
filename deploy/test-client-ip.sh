#!/usr/bin/env bash
# Prove, with the real pinned Caddy image, what the game server receives in X-Client-IP.
#
# It builds a throwaway copy of deploy/Caddyfile that listens on plain HTTP on loopback and
# points at a tiny upstream that echoes the headers it gets, then sends requests that pretend
# to come from Cloudflare (by adding 127.0.0.1 to the trusted list in the copy) and from
# someone else (the real list only), each with a forged and a genuine CF-Connecting-IP.
#
# Needs podman and python3; pulls the Caddy image named in deploy/Containerfile.caddy.
# Nothing in deploy/ is modified. Run from anywhere.
set -euo pipefail
cd "$(dirname "$0")"

image="$(sed -n 's/^FROM \(docker.io\/library\/caddy[^ ]*\).*/\1/p' Containerfile.caddy)"
[ -n "$image" ] || { echo "no Caddy image in Containerfile.caddy" >&2; exit 1; }
work="$(mktemp -d)"
pids=()
cleanup() {
	for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
	podman rm -f mud-clientip-test >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

upstream_port=18391
caddy_port=18390

cat > "$work/upstream.py" <<'PY'
import http.server, json, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"x_client_ip": self.headers.get("X-Client-IP"),
                           "cf": self.headers.get("CF-Connecting-IP"),
                           "peer": self.client_address[0]}).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
python3 "$work/upstream.py" "$upstream_port" & pids+=($!)

# Run Caddy with a copy of the real Caddyfile. $1 is "trusted" (127.0.0.1 counts as Cloudflare)
# or "untrusted" (only the published ranges do).
run_caddy() {
	local mode="$1"
	podman rm -f mud-clientip-test >/dev/null 2>&1 || true
	mkdir -p "$work/$mode"
	sed "s#127.0.0.1:8080#127.0.0.1:$upstream_port#; s#^{\$#{\n\tadmin off#" Caddyfile > "$work/$mode/Caddyfile"
	if [ "$mode" = trusted ]; then
		sed 's#trusted_proxies static#trusted_proxies static 127.0.0.1/32#' cloudflare-ips.caddy > "$work/$mode/cloudflare-ips.caddy"
	else
		cp cloudflare-ips.caddy "$work/$mode/cloudflare-ips.caddy"
	fi
	podman run -d --rm --name mud-clientip-test --network host -v "$work/$mode:/etc/caddy:ro,Z" \
		-e "MUD_DOMAIN=http://127.0.0.1:$caddy_port" -e ACME_EMAIL=test@invalid.invalid "$image" >/dev/null
	for _ in $(seq 1 50); do
		curl -fsS -o /dev/null "http://127.0.0.1:$caddy_port/ws" 2>/dev/null && return 0
		sleep 0.2
	done
	podman logs mud-clientip-test >&2; echo "caddy did not start" >&2; return 1
}

failed=0
check() { # name expected-json-field expected-value actual
	if [ "$3" = "$4" ]; then echo "ok   $1"; else echo "FAIL $1: $2 = '$4', want '$3'" >&2; failed=1; fi
}
ask() { curl -fsS "$@" "http://127.0.0.1:$caddy_port/ws"; }
field() { python3 -c 'import json,sys; print(json.load(sys.stdin).get(sys.argv[1]) or "")' "$1"; }

echo "== connection from a trusted proxy (stands in for Cloudflare)"
run_caddy trusted
check "genuine CF-Connecting-IP becomes X-Client-IP" x_client_ip 203.0.113.7 "$(ask -H 'CF-Connecting-IP: 203.0.113.7' | field x_client_ip)"
check "IPv6 client address passes through" x_client_ip 2001:db8::9 "$(ask -H 'CF-Connecting-IP: 2001:db8::9' | field x_client_ip)"
check "a client-supplied X-Client-IP is overwritten" x_client_ip 203.0.113.7 "$(ask -H 'CF-Connecting-IP: 203.0.113.7' -H 'X-Client-IP: 6.6.6.6' | field x_client_ip)"
check "no CF-Connecting-IP falls back to the peer" x_client_ip 127.0.0.1 "$(ask | field x_client_ip)"

echo "== connection from anyone else (the origin hit directly)"
run_caddy untrusted
check "a forged CF-Connecting-IP is ignored" x_client_ip 127.0.0.1 "$(ask -H 'CF-Connecting-IP: 6.6.6.6' | field x_client_ip)"
check "a forged X-Client-IP is overwritten" x_client_ip 127.0.0.1 "$(ask -H 'X-Client-IP: 6.6.6.6' | field x_client_ip)"
check "both forged at once" x_client_ip 127.0.0.1 "$(ask -H 'CF-Connecting-IP: 6.6.6.6' -H 'X-Client-IP: 7.7.7.7' | field x_client_ip)"

[ "$failed" = 0 ] && echo "all client-IP checks passed" || { echo "client-IP checks FAILED" >&2; exit 1; }
