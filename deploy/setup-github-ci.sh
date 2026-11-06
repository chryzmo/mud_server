#!/usr/bin/env bash
# One-time setup for CI deploys. Run locally, from the mud_server repo, with gh
# logged in and SSH access to the Droplet as deploy:
#
#   deploy/setup-github-ci.sh <droplet IP> [owner/repo ...]
#
# With no repositories it sets up mud_server and mud_frontend. Name repositories
# to set up only those (for example a new one: chryzmo/mud_content). Each run
# generates a new CI key and stores it only in the repositories it names, so
# running it for one repository leaves the others' keys untouched; the old
# keys stay authorized on the host until you remove them from
# ~deploy/.ssh/authorized_keys.
#
# 1. Generates an SSH key just for CI and authorizes it for `deploy` on the host
#    (restricted: no port forwarding, agent forwarding or PTY).
# 2. Creates a "production" environment in each repository (an existing one and
#    its other secrets are left alone) and stores the private key, the host's SSH
#    host key and the deploy target as environment secrets.
# 3. Turns auto-deploy on (repository variable AUTO_DEPLOY=true) in each.
# The private key exists only in a temp directory and in GitHub; it's deleted
# from this machine at the end.
set -euo pipefail

ip="${1:?usage: deploy/setup-github-ci.sh <droplet IP> [owner/repo ...]}"
shift
repos=("$@")
if [ "${#repos[@]}" -eq 0 ]; then
	repos=(chryzmo/mud_server chryzmo/mud_frontend)
fi
for repo in "${repos[@]}"; do
	case "$repo" in
		*/*) ;;
		*) echo "setup-github-ci.sh: '$repo' is not owner/repo" >&2; exit 2 ;;
	esac
done
host="deploy@$ip"

known_hosts="$(ssh-keygen -F "$ip" | grep -v '^#' || true)"
if [ -z "$known_hosts" ]; then
	echo "No known_hosts entry for $ip; connect once with 'ssh $host true' and check the fingerprint." >&2
	exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
ssh-keygen -q -t ed25519 -N '' -C "github-actions-deploy@mud" -f "$tmp/ci_key"

echo "== Authorizing the CI key on $host"
printf 'restrict %s\n' "$(cat "$tmp/ci_key.pub")" |
	ssh -o BatchMode=yes "$host" 'umask 077; cat >> ~/.ssh/authorized_keys'
ssh -o BatchMode=yes -o IdentitiesOnly=yes -i "$tmp/ci_key" "$host" true
echo "   CI key works"

for repo in "${repos[@]}"; do
	echo "== $repo"
	gh api -X PUT "repos/$repo/environments/production" > /dev/null
	gh secret set DEPLOY_SSH_KEY --repo "$repo" --env production < "$tmp/ci_key"
	printf '%s\n' "$known_hosts" | gh secret set DEPLOY_KNOWN_HOSTS --repo "$repo" --env production
	printf '%s' "$host" | gh secret set DEPLOY_HOST --repo "$repo" --env production
	gh variable set AUTO_DEPLOY --repo "$repo" --body true
done
echo "Done. Flip AUTO_DEPLOY to false in a repo to freeze its auto-deploys."
