#!/usr/bin/env bash
# Deploy to the Droplet: build images locally, ship them, restart the stack.
#
#   deploy/deploy.sh             deploy the current commits of mud_server and ../mud_frontend
#   deploy/deploy.sh rollback    switch back to the previously deployed images
#   deploy/deploy.sh status      show service status and recent game server logs
#
# Settings come from the environment or from deploy/deploy.local.env (gitignored):
#   MUD_DEPLOY_HOST  ssh target, e.g. deploy@<droplet IP>. Required. Use the IP, not the
#                    domain: the domain goes through Cloudflare's proxy, which doesn't carry SSH.
#   MUD_DOMAIN       public domain (default magmud.crmoore.net)
#   ACME_EMAIL       Let's Encrypt contact; only needed on the first deploy, to
#                    create ~/mud/caddy.env on the host. Kept out of git.
#   ALLOW_DIRTY=1    deploy even with uncommitted changes (tags the images -dirty)
#   SKIP_TESTS=1     don't run the test suites (CI sets this: its test job already passed);
#                    the frontend is still built
#
# Deploys take a lock on the host (~/mud/deploy.lock), so a deploy triggered from each
# repo at the same time runs one after the other.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
[ -f deploy/deploy.local.env ] && . deploy/deploy.local.env
host="${MUD_DEPLOY_HOST:?set MUD_DEPLOY_HOST (e.g. in deploy/deploy.local.env)}"
domain="${MUD_DOMAIN:-magmud.crmoore.net}"
frontend="$(cd ../mud_frontend && pwd)"
images=(mud-db mud-server mud-caddy)

remote() { ssh -o BatchMode=yes "$host" "$@"; }

take_lock() {
	remote 'mkdir -p ~/mud
		for _ in $(seq 1 120); do
			mkdir ~/mud/deploy.lock 2> /dev/null && exit 0
			# A lock older than 20 minutes was left by a deploy that died; take it over.
			if [ -n "$(find ~/mud/deploy.lock -maxdepth 0 -mmin +20)" ]; then
				rmdir ~/mud/deploy.lock
				continue
			fi
			sleep 5
		done
		echo "deploy: another deploy has held the lock for 10 minutes" >&2
		exit 1'
	trap 'remote "rmdir ~/mud/deploy.lock" 2> /dev/null || true' EXIT
}

case "${1:-deploy}" in
	status)
		remote 'systemctl --user --no-pager list-units "mud*" ; echo; journalctl --user -u mud-server -n 20 --no-pager -o cat'
		exit 0
		;;
	rollback)
		take_lock
		remote 'set -e
			for img in mud-db mud-server mud-caddy; do
				if podman image exists "localhost/$img:previous"; then
					podman tag "localhost/$img:previous" "localhost/$img:current"
				fi
			done
			systemctl --user restart mud-db mud-server mud-caddy'
		echo "deploy: rolled back to the previous images"
		exit 0
		;;
	deploy) ;;
	*)
		echo "usage: deploy/deploy.sh [deploy|rollback|status]" >&2
		exit 2
		;;
esac

echo "== Checking working trees and tests"
suffix=""
for repo in . "$frontend"; do
	if [ -n "$(git -C "$repo" status --porcelain)" ]; then
		if [ "${ALLOW_DIRTY:-}" = 1 ]; then
			suffix="-dirty"
		else
			echo "deploy: $repo has uncommitted changes (ALLOW_DIRTY=1 to deploy anyway)" >&2
			exit 1
		fi
	fi
done
if [ "${SKIP_TESTS:-}" = 1 ]; then
	echo "   skipping tests (SKIP_TESTS=1)"
	(cd "$frontend" && npm ci --silent --no-audit --no-fund && npx vite build --logLevel warn)
else
	scripts/test.sh > /dev/null
	"$frontend/scripts/test.sh" > /dev/null # also builds $frontend/dist
fi
tag="$(git rev-parse --short HEAD)-$(git -C "$frontend" rev-parse --short HEAD)$suffix"

echo "== Building images ($tag)"
# --timestamp 0 makes builds reproducible: an unchanged image keeps its ID, so it isn't re-uploaded.
build=(podman build -q --timestamp 0)
"${build[@]}" -f deploy/Containerfile.server -t "localhost/mud-server:$tag" . > /dev/null
"${build[@]}" -f deploy/Containerfile.db -t "localhost/mud-db:$tag" . > /dev/null
"${build[@]}" -f deploy/Containerfile.caddy --build-context "frontend=$frontend/dist" \
	-t "localhost/mud-caddy:$tag" . > /dev/null

take_lock

echo "== Shipping images to $host"
for img in "${images[@]}"; do
	id="$(podman image inspect --format '{{.Id}}' "localhost/$img:$tag")"
	if remote "podman image exists $id"; then
		echo "   $img: already on the host"
	else
		echo "   $img: uploading $(podman image inspect --format '{{.Size}}' "localhost/$img:$tag" | numfmt --to=iec)"
		podman save "localhost/$img:$tag" | gzip -1 | remote 'gunzip | podman load -q > /dev/null'
	fi
	remote "podman tag $id localhost/$img:$tag"
done

echo "== Installing units and config"
remote 'mkdir -p ~/mud ~/.config/containers/systemd'
tar -C deploy/quadlet -cf - . | remote 'tar -C ~/.config/containers/systemd -xf -'
remote 'test -f ~/mud/secrets.env || {
	umask 077
	pw="$(openssl rand -hex 24)"
	printf "POSTGRES_PASSWORD=%s\nMUD_DATABASE_URL=postgres://mud:%s@127.0.0.1:5432/mud\n" "$pw" "$pw" > ~/mud/secrets.env
	echo "   created ~/mud/secrets.env"
}'
if ! remote 'test -f ~/mud/caddy.env'; then
	if [ -z "${ACME_EMAIL:-}" ]; then
		echo "deploy: first deploy needs ACME_EMAIL to create ~/mud/caddy.env on the host" >&2
		exit 1
	fi
	printf 'MUD_DOMAIN=%s\nACME_EMAIL=%s\n' "$domain" "$ACME_EMAIL" | remote 'umask 077; cat > ~/mud/caddy.env'
	echo "   created ~/mud/caddy.env"
fi

echo "== Switching to $tag"
changed="$(remote "set -e
	for img in ${images[*]}; do
		new=\$(podman image inspect --format '{{.Id}}' localhost/\$img:$tag)
		cur=\$(podman image inspect --format '{{.Id}}' localhost/\$img:current 2>/dev/null || true)
		if [ \"\$new\" != \"\$cur\" ]; then
			[ -n \"\$cur\" ] && podman tag localhost/\$img:current localhost/\$img:previous
			podman tag localhost/\$img:$tag localhost/\$img:current
			echo \$img
		fi
	done")"
remote 'systemctl --user daemon-reload'
db_restarted=""
for img in "${images[@]}"; do
	if [ -n "$changed" ] && grep -qx "$img" <<< "$changed"; then
		# mud-server has Requires=mud-db, so restarting the database already
		# restarted the server (on its new image, since the tags have switched).
		# Restarting it again would disconnect everyone twice.
		if [ "$img" = mud-server ] && [ -n "$db_restarted" ]; then
			echo "   mud-server restarted along with mud-db"
			continue
		fi
		echo "   restarting $img"
		remote "systemctl --user restart $img"
		[ "$img" = mud-db ] && db_restarted=1
	else
		remote "systemctl --user start $img"
	fi
done

echo "== Health check"
for _ in $(seq 1 30); do
	if [ "$(remote 'podman exec mud-caddy wget -qO- http://127.0.0.1:8080/ 2>/dev/null')" = "Welcome!" ]; then
		echo "   game server answering inside the pod"
		break
	fi
	sleep 1
done
if curl -fsS -o /dev/null --max-time 10 "https://$domain/"; then
	echo "   https://$domain/ is up"
else
	echo "deploy: https://$domain/ is not reachable yet (DNS or the TLS certificate may still be pending)" >&2
fi
echo "== Deployed $tag"
