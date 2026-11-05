#!/usr/bin/env bash
# One-time setup for a fresh Fedora host (written for Fedora 44 on DigitalOcean).
# Safe to re-run. Run as root:
#
#   ssh root@HOST 'bash -s' < deploy/bootstrap-host.sh
#
# Two users get root's SSH keys: `admin` (sudo, for maintenance) and `deploy`
# (no sudo; runs the rootless containers and is what CI logs in as). It does not
# turn off root SSH login: confirm `ssh admin@HOST sudo -n true` works first,
# then run deploy/harden-ssh.sh.
set -euo pipefail

ADMIN_USER=admin
DEPLOY_USER=deploy
HOSTNAME_FQDN=magmud

echo "== System updates"
dnf -y -q upgrade --refresh

echo "== Packages"
dnf -y -q install podman firewalld dnf5-plugin-automatic jq

echo "== Hostname"
hostnamectl set-hostname "$HOSTNAME_FQDN"

echo "== Admin user (sudo) and deploy user (no sudo)"
for user in "$ADMIN_USER" "$DEPLOY_USER"; do
	id "$user" > /dev/null 2>&1 || useradd --create-home "$user"
	install -d -m 700 -o "$user" -g "$user" "/home/$user/.ssh"
	[ -s "/home/$user/.ssh/authorized_keys" ] ||
		install -m 600 -o "$user" -g "$user" /root/.ssh/authorized_keys "/home/$user/.ssh/authorized_keys"
done
usermod -aG wheel "$ADMIN_USER"
echo "$ADMIN_USER ALL=(ALL) NOPASSWD: ALL" > "/etc/sudoers.d/90-$ADMIN_USER"
chmod 440 "/etc/sudoers.d/90-$ADMIN_USER"
visudo -cq
# Rootless containers for the deploy user keep running without a login session.
loginctl enable-linger "$DEPLOY_USER"

echo "== Let rootless Caddy bind ports 80 and 443"
echo 'net.ipv4.ip_unprivileged_port_start = 80' > /etc/sysctl.d/90-unprivileged-ports.conf
sysctl -q --system

echo "== Firewall: SSH, HTTP, HTTPS (TCP and UDP for HTTP/3) only"
systemctl enable --now firewalld
firewall-cmd -q --permanent --zone=public --add-service=ssh
firewall-cmd -q --permanent --zone=public --add-service=http
firewall-cmd -q --permanent --zone=public --add-service=https
firewall-cmd -q --permanent --zone=public --add-port=443/udp
for svc in dhcpv6-client mdns cockpit; do
	firewall-cmd -q --permanent --zone=public --remove-service="$svc" 2> /dev/null || true
done
firewall-cmd -q --reload

echo "== Automatic security updates (no automatic reboots)"
cat > /etc/dnf/automatic.conf <<'CONF'
[commands]
upgrade_type = security
download_updates = yes
apply_updates = yes
reboot = never

[emitters]
emit_via = stdio
CONF
systemctl enable --now dnf5-automatic.timer

echo "== Disk swap as a safety margin behind zram"
if ! swapon --show=NAME --noheadings | grep -qx /swapfile; then
	if [ "$(findmnt -no FSTYPE /)" = btrfs ]; then
		btrfs filesystem mkswapfile --size 1G /swapfile > /dev/null
	else
		fallocate -l 1G /swapfile
		chmod 600 /swapfile
		mkswap -q /swapfile
	fi
	grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap defaults,pri=10 0 0' >> /etc/fstab
	swapon -p 10 /swapfile
fi

latest_kernel="$(rpm -q kernel-core --qf '%{VERSION}-%{RELEASE}.%{ARCH}\n' | sort -V | tail -1)"
if [ "$(uname -r)" = "$latest_kernel" ]; then
	echo "== Done."
else
	echo "== Done. Reboot to switch to kernel $latest_kernel (running $(uname -r))."
fi
