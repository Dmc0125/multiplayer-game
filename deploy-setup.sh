#!/bin/bash

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

info "Setting up deploy user"

if ! id -u deploy &>/dev/null; then
    useradd -m -s /bin/bash deploy
    rsync --archive --chown=deploy:deploy ~/.ssh /home/deploy/.ssh
fi

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

info "Enabling services"

systemctl daemon-reload
systemctl enable paddle-game.server
systemctl enable paddle-game.client

cat > /etc/sudoers.d/deploy <<EOF
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart paddle-game.server
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart paddle-game.client
EOF

success "Setup complete! Now place your .env in $app_dir/shared/server.env and $app_dir/shared/client.env"

