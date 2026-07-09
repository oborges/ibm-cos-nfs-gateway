# Linux Service Installation

This guide installs IBM Cloud COS NFS Gateway as a `systemd` service for a
Linux server operator.

## What The Installer Creates

- `/usr/local/bin/nfs-gateway`
- `/etc/nfs-gateway/config.yaml`
- `/etc/default/nfs-gateway`
- `/etc/systemd/system/nfs-gateway.service`
- `nfs-gateway` system user and group
- `/var/cache/nfs-gateway`
- `/var/staging/nfs-gateway`
- `/var/log/nfs-gateway`

The service runs as the unprivileged `nfs-gateway` user and receives only
`CAP_NET_BIND_SERVICE`, which lets it bind the default NFS port `2049`.

## Install From Source

```bash
git clone https://github.com/oborges/ibm-cos-nfs-gateway.git
cd ibm-cos-nfs-gateway
sudo ./scripts/install-linux-service.sh --build
```

To install a prebuilt binary instead:

```bash
sudo ./scripts/install-linux-service.sh --binary /path/to/nfs-gateway
```

The installer does not overwrite an existing `/etc/nfs-gateway/config.yaml`
unless `--force-config` is passed.

## Configure

Edit the service config:

```bash
sudoedit /etc/nfs-gateway/config.yaml
```

Set at least:

```yaml
cos:
  endpoint: "s3.us-south.cloud-object-storage.appdomain.cloud"
  bucket: "my-nfs-bucket"
  region: "us-south"
  auth_type: "iam"
  api_key: "your-ibm-cloud-api-key"
```

Secrets may also be placed in `/etc/default/nfs-gateway`:

```bash
NFS_GATEWAY_COS_API_KEY=your-ibm-cloud-api-key
```

The installer sets config and environment file permissions to `0640` with group
`nfs-gateway`.

## Start And Inspect

```bash
sudo systemctl enable --now nfs-gateway
sudo systemctl status nfs-gateway
sudo journalctl -u nfs-gateway -f
```

To install and start in one command:

```bash
sudo ./scripts/install-linux-service.sh --build --enable --start
```

## Mount From The Gateway Host

```bash
sudo mkdir -p /mnt/cos-nfs
sudo mount -t nfs4 -o vers=4.0,tcp,port=2049 localhost:/ /mnt/cos-nfs
```

## Custom Cache Or Staging Paths

The service unit uses `ProtectSystem=strict`, so only the default writable paths
are open:

- `/var/cache/nfs-gateway`
- `/var/staging/nfs-gateway`
- `/var/log/nfs-gateway`

If you move `cache.data.path`, `staging.root_dir`, or file logging to another
directory, add a systemd drop-in:

```bash
sudo systemctl edit nfs-gateway
```

Example:

```ini
[Service]
ReadWritePaths=/mnt/nfs-gateway-cache /mnt/nfs-gateway-staging
```

Then create and assign the directories:

```bash
sudo install -d -m 0750 -o nfs-gateway -g nfs-gateway /mnt/nfs-gateway-cache
sudo install -d -m 0750 -o nfs-gateway -g nfs-gateway /mnt/nfs-gateway-staging
sudo systemctl daemon-reload
sudo systemctl restart nfs-gateway
```

## Upgrade

From a fresh checkout or release directory:

```bash
sudo ./scripts/install-linux-service.sh --build
sudo systemctl restart nfs-gateway
```

Existing config and environment files are preserved by default.
