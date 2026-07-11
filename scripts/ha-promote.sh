#!/usr/bin/env bash
# Promote this standby gateway to active.
#
# Prerequisites: the gateway binary, /etc/nfs-gateway/config.yaml (ha.enabled:
# true), and staging replication via nfs-gateway-replicate.timer.
#
# Fencing rules enforced by the gateway itself:
#   - dead primary (lease stale, > ha.lease_timeout):    promotion proceeds
#   - graceful primary shutdown (lease released):        promotion proceeds
#   - primary still alive (lease fresh):                 promotion REFUSED
# Use --force only when the primary is confirmed dead but its lease has not
# yet gone stale; it sets NFS_GATEWAY_HA_FORCE_TAKEOVER for this start only.
set -euo pipefail

FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

echo "==> Stopping staging replication (this node is becoming the source of truth)"
systemctl disable --now nfs-gateway-replicate.timer 2>/dev/null || true
systemctl stop nfs-gateway-replicate.service 2>/dev/null || true

echo "==> Final ownership pass on replicated staging state"
chown -R nfs-gateway:nfs-gateway /var/staging/nfs-gateway

if [ "$FORCE" = "1" ]; then
  echo "==> FORCE TAKEOVER requested: stealing the lease even if fresh"
  mkdir -p /etc/systemd/system/nfs-gateway.service.d
  printf "[Service]\nEnvironment=NFS_GATEWAY_HA_FORCE_TAKEOVER=true\n" \
    > /etc/systemd/system/nfs-gateway.service.d/ha-force.conf
  systemctl daemon-reload
fi

echo "==> Starting gateway (lease acquisition decides whether promotion is allowed)"
systemctl enable --now nfs-gateway

HEALTHY=0
for i in $(seq 1 30); do
  if curl -sf http://127.0.0.1:8081/health/live >/dev/null 2>&1; then
    HEALTHY=1
    break
  fi
  if systemctl is-failed --quiet nfs-gateway; then
    break
  fi
  sleep 2
done

if [ "$HEALTHY" != "1" ]; then
  echo "!! Promotion refused or failed. Most recent gateway log:"
  journalctl -u nfs-gateway --no-pager | grep -E "lease|fatal" | tail -3
  echo "==> Stopping the refused gateway (it must not keep retrying)"
  systemctl disable --now nfs-gateway 2>/dev/null || true
  systemctl reset-failed nfs-gateway 2>/dev/null || true
  if [ "$FORCE" = "0" ]; then
    echo "!! If the primary is confirmed DEAD, re-run with --force."
  fi
  exit 1
fi

# One-shot force must never persist across restarts.
if [ "$FORCE" = "1" ]; then
  rm -f /etc/systemd/system/nfs-gateway.service.d/ha-force.conf
  systemctl daemon-reload
fi

echo "==> Promotion complete. This node is the active gateway."
echo "    Point clients here (remount): mount -t nfs4 -o vers=4.0 $(hostname -I | awk '{print $1}'):/ /mnt/cos-nfs"
journalctl -u nfs-gateway --no-pager | grep "HA lease acquired" | tail -1

# Made with Bob
