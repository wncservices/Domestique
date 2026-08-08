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

# A local SQLite library with the example route loaded, for a look around.
demo:
    go run ./apps/api/cmd/domestique import --db ./data/demo.db --from examples/routes
    go run ./apps/api/cmd/domestique serve --db ./data/demo.db

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

# Write a route out as a FIT course, to copy onto a device over USB.
fit SLUG *ARGS:
    go run ./apps/api/cmd/domestique fit {{SLUG}} {{ARGS}}

# Load a folder of .gpx files into the database. A one-off, not a storage mode.
import FROM:
    go run ./apps/api/cmd/domestique import --db ./data/domestique.db --from {{FROM}}

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
