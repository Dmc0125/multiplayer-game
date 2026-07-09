#!/bin/bash

domain=$1

if [ -z "$domain" ]; then
    echo "Usage: $0 <domain>"
    exit 1
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

info() {
    echo -e "${BLUE}==> ${RESET}$1"
}

success() {
    echo -e "${GREEN}✓✓✓ ${RESET}$1"
}

set -euo pipefail

info "Setting up app directory"

app_dir="/opt/paddle-game"

if [ ! -d $app_dir ]; then
    mkdir -p $app_dir/{releases,current,shared}
    chown -R deploy:deploy $app_dir
fi

info "Setting up postgres"

cat > $app_dir/docker-compose.yml <<EOF
services:
  db:
    image: postgres:17-alpine
    container_name: paddle-db
    environment:
      POSTGRES_USER: user
      POSTGRES_PASSWORD: pwd
      POSTGRES_DB: game
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - ./data:/var/lib/postgresql/data
EOF

cd $app_dir
docker compose up -d &> /dev/null
retries=0
while ! docker compose ps | grep -q "paddle-db.*Up"; do
    sleep 1
    retries=$((retries + 1))
    if [ $retries -gt 10 ]; then
        echo "${RED}✗ Error:${RESET} Timed out waiting for postgres to start"
        exit 1
    fi
done

success "Postgres started"
info "Setting up server service"

cat > /etc/systemd/system/paddle-game.server.service <<EOF
[Unit]
Description=Paddle game server
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=deploy
WorkingDirectory=$app_dir
ExecStart=$app_dir/current/server
Restart=always
RestartSec=3
EnvironmentFile=$app_dir/shared/server.env

[Install]
WantedBy=multi-user.target
EOF

info "Setting up client service"

cat > /etc/systemd/system/paddle-game.client.service <<EOF
[Unit]
Description=Paddle game client
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=deploy
WorkingDirectory=$app_dir
ExecStart=node $app_dir/current/client/server/entry.mjs
Restart=always
RestartSec=3
EnvironmentFile=$app_dir/shared/client.env

[Install]
WantedBy=multi-user.target
EOF

info "Setting up nginx"

cat > /etc/nginx/sites-available/$domain <<EOF
server {
        listen 80;
        server_name $domain;

        return 301 https://\$host\$request_uri;
}

server {
        listen 443 ssl;
        server_name $domain;

        ssl_certificate /etc/ssl/cloudflare/paddle.crt;
        ssl_certificate_key /etc/ssl/cloudflare/paddle.key;

        access_log /var/log/nginx/paddle_access.log;
        error_log /var/log/nginx/paddle_error.log warn;

        add_header X-Content-Type-Options nosniff;
        add_header X-Frame-Options SAMEORIGIN;
        add_header Referrer-Policy strict-origin-when-cross-origin;

        location /api {
                proxy_pass         http://localhost:8080;
                proxy_http_version 1.1;
                proxy_set_header   Host              \$host;
                proxy_set_header   X-Real-IP         \$remote_addr;
                proxy_set_header   X-Forwarded-For   \$proxy_add_x_forwarded_for;
                proxy_set_header   X-Forwarded-Proto https;
                proxy_set_header   Upgrade           \$http_upgrade;
                proxy_set_header   Connection        'upgrade';
        }

        location / {
                proxy_pass         http://localhost:4321;
                proxy_http_version 1.1;
                proxy_set_header   Host              \$host;
                proxy_set_header   X-Real-IP         \$remote_addr;
                proxy_set_header   X-Forwarded-For   \$proxy_add_x_forwarded_for;
                proxy_set_header   X-Forwarded-Proto https;
        }
}
EOF

ln -sf /etc/nginx/sites-available/$domain /etc/nginx/sites-enabled/$domain

info "Enabling services"

systemctl daemon-reload
systemctl enable paddle-game.server
systemctl enable paddle-game.client
systemctl reload nginx

cat > /etc/sudoers.d/deploy <<EOF
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart paddle-game.server
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart paddle-game.client
EOF

success "Setup complete! Now place your .env in $app_dir/shared/server.env and $app_dir/shared/client.env"
echo ""
echo "
==> Nginx

Nginx access logs are in /var/log/nginx/paddle_access.log
Nginx error logs are in /var/log/nginx/paddle_error.log

==> Next steps:

Create an origin server certificate on cloudflare and create these files:
    => /etc/ssl/cloudflare/paddle.crt - contains the certificate
    => /etc/ssl/cloudflare/paddle.key - contains the private key

Place env variables in:
    => /opt/paddle-game/shared/server.env - example in server/example.env
    => /opt/paddle-game/shared/client.env - example in client/example.env
"




