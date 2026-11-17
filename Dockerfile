# Bard's primary install is a static binary under systemd; see scripts/install.
# This image is for hosts already running containers.
FROM golang:1.27-alpine AS build

WORKDIR /src
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /bard ./cmd/bard

# Docker seeds a fresh named volume from the image, ownership included, so the
# data directory has to exist here owned by the runtime uid. Without it the
# volume arrives root-owned and the server cannot open its own database.
RUN mkdir -p /seed/var/lib/bard && chown -R 2273:2273 /seed/var/lib/bard

# Scratch is viable because nothing here links against libc. The certificates
# are the one thing the binary cannot carry itself, and metadata lookups need
# them.
FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /seed/ /
COPY --from=build /bard /bard

# Matches the uid the compose file chowns its volume to.
USER 2273:2273

EXPOSE 2273
VOLUME /var/lib/bard

ENTRYPOINT ["/bard"]
CMD ["serve", "--config", "/etc/bard/bard.yml"]
