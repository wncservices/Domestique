default:
    @just --list

# --- setup ---

install:
    npm install

# --- dev ---

# Run the API (serves the built frontend if apps/web/dist exists).
api *ARGS:
    go run ./apps/api/cmd/domestique serve {{ARGS}}

# Run the Vue dev server with hot reload, proxying /api to the API on :8080.
web:
    npm --workspace @domestique/web run dev

# --- CLI ---

validate *ARGS:
    go run ./apps/api/cmd/domestique validate {{ARGS}}

plan *ARGS:
    go run ./apps/api/cmd/domestique plan {{ARGS}}

push *ARGS:
    go run ./apps/api/cmd/domestique push {{ARGS}}

# --- quality ---

fmt:
    gofmt -w apps/api
    go vet ./apps/api/...

lint: fmt
    npm --workspace @domestique/web run typecheck

test:
    go test ./apps/api/...

check: lint test

# --- build ---

build: build-web build-api

build-web:
    npm --workspace @domestique/web run build

build-api:
    go build -o bin/domestique ./apps/api/cmd/domestique

docker:
    docker build -t domestique:dev .
