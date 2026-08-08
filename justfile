default:
    @just --list

# --- setup ---

install:
    npm install
    cp -n domestique.example.yaml domestique.yaml || true

# --- dev ---

# Run the API (serves the built frontend if apps/web/dist exists).
api *ARGS:
    go run ./apps/api/cmd/domestique serve {{ARGS}}

# Run the API against the bundled example routes — no config needed.
demo:
    go run ./apps/api/cmd/domestique serve --source fs --library examples/routes

# Run the API against a local SQLite library, with uploads enabled.
demo-db:
    go run ./apps/api/cmd/domestique serve --source db --db ./data/domestique.db

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

# List or import routes from Komoot. Needs KOMOOT_EMAIL and KOMOOT_PASSWORD.
komoot *ARGS:
    go run ./apps/api/cmd/domestique komoot {{ARGS}}

# Copy a directory of GPX routes into a database library.
import FROM:
    go run ./apps/api/cmd/domestique import --source db --db ./data/domestique.db --from {{FROM}}

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
