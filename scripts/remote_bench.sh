#!/bin/bash
set -x

echo "Terminating old gateways..."
sudo pkill -9 -f nfs-gateway || true
sudo umount -f /mnt/cos-nfs || true
sudo rm -rf /tmp/nfs-staging || true
sudo install -d -m 700 -o root -g root /tmp/nfs-staging

echo "Spawning Gateway daemon natively..."
cd /home/vpcuser/ibm-cos-nfs-gateway
sudo env NFS_GATEWAY_STAGING_ENABLED=true NFS_GATEWAY_STAGING_ROOT_DIR=/tmp/nfs-staging ./bin/nfs-gateway --config configs/config.yaml > /tmp/nfs.log 2>&1 &
sleep 5

echo "Mounting natively to COS..."
sudo mount -t nfs -o vers=3,tcp,nolock,mountport=2049,port=2049 localhost:/ /mnt/cos-nfs

echo "Executing custom metrics evaluation dashboard..."
sudo ./scripts/run_stress_tests.sh
