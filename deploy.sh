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

project_dir=$1
server_ip=$2

if [ -z "$project_dir" ] || [ -z "$server_ip" ] ; then
  echo "Usage: $0 <project_dir> <server_ip>"
  exit 1
fi

app_dir="/opt/paddle-game"
timestamp=$(date +%Y_%m_%d_%H_%M_%S)

info "Building go server..."
cd $project_dir/server
GOOS=linux GOARCH=amd64 go build -o /tmp/server_$timestamp ./src

info "Building astro client..."

cd $project_dir/client
pnpm ci
pnpm build

info "Copying files into server:releases/r_$timestamp"
ssh deploy@$server_ip mkdir -p $app_dir/releases/r_$timestamp/client

# copy the server build into release/server
rsync -az -e ssh /tmp/server_$timestamp deploy@$server_ip:$app_dir/releases/r_$timestamp/server
# copy the contents of dist/ into release/client
rsync -az -e ssh $project_dir/client/dist/ deploy@$server_ip:$app_dir/releases/r_$timestamp/client

rm /tmp/server_$timestamp

info "Swapping release and restarting..."

ssh deploy@$server_ip <<EOF
ln -sf $app_dir/releases/r_$timestamp/server $app_dir/current/server
ln -sfn $app_dir/releases/r_$timestamp/client $app_dir/current/client
sudo systemctl restart paddle-game.server
sudo systemctl restart paddle-game.client
EOF

info "Migrate database..."

rsync -az -e ssh $project_dir/db/migrator.bin deploy@$server_ip:$app_dir/migrator.bin
rsync -az -e ssh $project_dir/db/migrations/ deploy@$server_ip:$app_dir/migrations
ssh deploy@$server_ip "$app_dir/migrator.bin $app_dir/migrations up 999"

info "Cleaning old releases..."
ssh deploy@$server_ip "ls -dt $app_dir/releases/* | tail -n +6 | xargs rm -rf"

success "Deployed!"
