#!/usr/bin/env bash
# One-time migration for a host bootstrapped before the admin/deploy split:
# create `admin` with sudo and the same SSH keys as `deploy`. Run it as deploy,
# while deploy still has sudo:
#
#   ssh deploy@HOST 'bash -s' < deploy/add-admin-user.sh
#   ssh admin@HOST sudo -n true      # confirm before running drop-deploy-sudo.sh
set -euo pipefail

id admin > /dev/null 2>&1 || sudo useradd --create-home --groups wheel admin
sudo install -d -m 700 -o admin -g admin /home/admin/.ssh
sudo install -m 600 -o admin -g admin /home/deploy/.ssh/authorized_keys /home/admin/.ssh/authorized_keys
echo "admin ALL=(ALL) NOPASSWD: ALL" | sudo tee /etc/sudoers.d/90-admin > /dev/null
sudo chmod 440 /etc/sudoers.d/90-admin
sudo visudo -cq
echo "admin created with sudo."
