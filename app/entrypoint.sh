#!/bin/bash
set -e

mkdir -p /app/data/logs /app/data/accounts
chmod 0700 /app/data/accounts

python3 -m web.cli bootstrap

exec /usr/local/bin/supervisord -c /app/supervisord.conf
