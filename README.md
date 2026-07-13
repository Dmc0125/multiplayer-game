# Paddle multiplayer game

## Runing the app

### Database

docker-compose file is located in the root directory of the project.

To run the database, run: `docker-compose up`

It will start the database on localhost:5432 with the following environment variables:

- `POSTGRES_USER`: user
- `POSTGRES_PASSWORD`: pwd
- `POSTGRES_DB`: game

It will also start the adminer web interface on `localhost:8081`.

### Server

Server expects environment variables as show in ./server/example.env

To run the server

```bash
# in the server directory
go run ./src
```

Server supports these flags:

- `-port`: port to listen on (default: `8080`)
- `-profile`: enable profiling (profiler will be started on `localhost:6060`)
- `-lobbies`: number of lobbies to run (default: `50`)
- `-logfile`: log file to write to (if provided, logs will be written to this file, otherwise to the console)

The app will start on `localhost:8080` and the server will be running on `localhost:8080/api/game`.

### Client

Client expects enviroment variables as shown in ./client/example.env

```bash
pnpm install
pnpm dev
```

## Testing

### Integration tests

Integration tests for lifecycle of a connection. For these tests to run, the server must be running
on localhost:8080.

```bash
go run ./server
```

```bash
go test -count=1 ./testing/lifecycle
```

### Stress tests

Stress tests for the server. These tests also expect the server to be running and the runner
supports these flags:

- `-url`: websocket url to connect to (default: `ws://localhost:8080/api/game`)
- `-duration`: duration of the test (default: `2m`)
- `-clients`: number of clients to run (default: `10`)
- `-ramp`: ramp up time for clients to connect (default: `20s`)

```bash
go test -count=1 ./testing/stress_test
```

While running the stress test and if the server was started with the `-profile` flag,
script `profile.sh` can be run in the project root directory. This script will start the
go profiler and open the pprof web interface in the browser.

```bash
bash ./profile.sh
```

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
