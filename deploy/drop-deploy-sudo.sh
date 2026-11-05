#!/usr/bin/env bash
# One-time migration, after add-admin-user.sh: take sudo away from `deploy`, so
# the key CI uses can't become root. Run it as admin:
#
#   ssh admin@HOST 'bash -s' < deploy/drop-deploy-sudo.sh
set -euo pipefail

sudo rm -f /etc/sudoers.d/90-deploy
sudo gpasswd -d deploy wheel > /dev/null 2>&1 || true
sudo visudo -cq
if sudo -l -U deploy 2> /dev/null | grep -q 'may run'; then
	echo "deploy still has sudo rules:" >&2
	sudo -l -U deploy >&2
	exit 1
fi
echo "deploy no longer has sudo."
