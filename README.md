# Paddle multiplayer game

## Deployment

### Prerequisites

1. Create a deploy user

```bash
sudo useradd -m -s /bin/bash deploy
# copy ssh keys
rsync --archive --chown=deploy:deploy ~/.ssh /home/deploy/.ssh
```

2. Install docker
3. Install nginx

### Setup

- Run `deploy-setup.sh <domain>` on the server to setup the deploy user and nginx

What the script does:

1. Creates app directory (/opt/paddle-game)
2. Sets up database
    - Creates docker-compose.yml
    - Starts postgres
3. Creates systemd services
    - paddle-game.server.service for the go server
    - paddle-game.client.service for the client
4. Creates nginx config (/etc/nginx/sites-available/<domain>)
5. Enables services
6. Creates sudoers file (/etc/sudoers.d/deploy) to allow restarting services

What needs to be done manually:

- Create an origin server certificate on cloudflare and create these files:
    - /etc/ssl/cloudflare/paddle.crt - contains the certificate
    - /etc/ssl/cloudflare/paddle.key - contains the private key

- Place env variables in:
    - /opt/paddle-game/shared/server.env
    - /opt/paddle-game/shared/client.env

### Continuous deployment

- Run `deploy.sh <project_dir> <server_ip>` locally to deploy the project

What the script does:

1. Builds the server and client locally
2. Copies the server binary to the deploy user
3. Copies the client build to the deploy user
4. Swaps the current release with the new one
5. Migrates the database using the src/db/migrator.bin
6. Cleans up old releases (keeps the last 6)
