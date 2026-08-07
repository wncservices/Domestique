# Frontend first: it changes more often than the Go deps, but the Go layer is
# the slower one to rebuild, so keep them in separate stages.
FROM node:25-alpine AS web
WORKDIR /src
COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/
RUN npm ci
COPY apps/web apps/web
RUN npm --workspace @domestique/web run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum apps/api/
RUN cd apps/api && go mod download
COPY apps/api apps/api
RUN cd apps/api && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/domestique ./cmd/domestique

FROM alpine:3.21
RUN apk add --no-cache ca-certificates git && adduser -D -u 10001 domestique
WORKDIR /app
COPY --from=api /out/domestique /usr/local/bin/domestique
COPY --from=web /src/apps/web/dist /app/web

USER domestique
EXPOSE 8080

# No route data is baked in. Mount one of:
#   fs  a checkout of your private routes repo at /app/routes
#   db  a volume at /app/data, and set source.kind=db in the config
# State lives on a volume too, so a restart does not re-push every route.
VOLUME ["/app/data"]

ENTRYPOINT ["domestique"]
CMD ["serve", "--addr", ":8080", "--config", "/app/domestique.yaml", \
     "--state", "/app/data/domestique-state.json", "--web-dir", "/app/web"]
