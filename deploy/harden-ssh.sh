#!/usr/bin/env bash
# Turn off root and password SSH login. Run as root only after confirming the
# deploy user can log in and use sudo:
#
#   ssh deploy@HOST sudo -n true && ssh root@HOST 'bash -s' < deploy/harden-ssh.sh
#
# The lowest-numbered drop-in wins in sshd, so 10- overrides cloud-init's 50-.
set -euo pipefail

cat > /etc/ssh/sshd_config.d/10-hardening.conf <<'CONF'
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
CONF
sshd -t
systemctl reload sshd
echo "Root and password SSH login disabled."
