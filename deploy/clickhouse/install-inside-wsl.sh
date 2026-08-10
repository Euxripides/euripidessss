#!/usr/bin/env bash
set -Eeuo pipefail

schema_file="${1:?schema file is required}"
: "${CLICKHOUSE_ETL_PASSWORD:?CLICKHOUSE_ETL_PASSWORD is required}"

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y apt-transport-https ca-certificates curl gnupg

install -d -m 0755 /usr/share/keyrings
curl -fsSL https://packages.clickhouse.com/rpm/lts/repodata/repomd.xml.key \
  | gpg --dearmor --yes -o /usr/share/keyrings/clickhouse-keyring.gpg
arch="$(dpkg --print-architecture)"
printf 'deb [signed-by=/usr/share/keyrings/clickhouse-keyring.gpg arch=%s] https://packages.clickhouse.com/deb stable main\n' "$arch" \
  > /etc/apt/sources.list.d/clickhouse.list
apt-get update
apt-get install -y clickhouse-server clickhouse-client

install -d -o clickhouse -g clickhouse -m 0750 \
  /var/lib/clickhouse/tmp \
  /var/lib/clickhouse/user_files \
  /var/lib/clickhouse/format_schemas \
  /var/lib/clickhouse/backups

cat > /etc/clickhouse-server/config.d/90-onchain-warehouse.xml <<'EOF'
<clickhouse>
    <listen_host>127.0.0.1</listen_host>
    <listen_host>::1</listen_host>
    <path replace="replace">/var/lib/clickhouse/</path>
    <tmp_path replace="replace">/var/lib/clickhouse/tmp/</tmp_path>
    <user_files_path replace="replace">/var/lib/clickhouse/user_files/</user_files_path>
    <format_schema_path replace="replace">/var/lib/clickhouse/format_schemas/</format_schema_path>
    <backups>
        <allowed_path>backups</allowed_path>
    </backups>
    <max_server_memory_usage_to_ram_ratio replace="replace">0.75</max_server_memory_usage_to_ram_ratio>
</clickhouse>
EOF

cat > /etc/wsl.conf <<'EOF'
[boot]
systemd=true

[automount]
enabled=true
options="metadata,umask=22,fmask=11"
EOF

systemctl enable clickhouse-server 2>/dev/null || true
systemctl restart clickhouse-server 2>/dev/null || service clickhouse-server restart

for _ in $(seq 1 60); do
  if clickhouse-client --query 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
clickhouse-client --query 'SELECT 1' >/dev/null

clickhouse-client --multiquery --query "
CREATE DATABASE IF NOT EXISTS onchain;
CREATE USER IF NOT EXISTS etl_app IDENTIFIED WITH sha256_password BY '${CLICKHOUSE_ETL_PASSWORD}' HOST IP '127.0.0.1', IP '::1';
ALTER USER etl_app IDENTIFIED WITH sha256_password BY '${CLICKHOUSE_ETL_PASSWORD}' HOST IP '127.0.0.1', IP '::1';
GRANT ALL ON onchain.* TO etl_app;
"

clickhouse-client --database onchain --multiquery < "$schema_file"
clickhouse-client --query "SELECT version(), currentDatabase()"
