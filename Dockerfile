# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.24-alpine AS build
WORKDIR /src

# Cache dependencies separately from the source code.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/devices-api ./cmd/api

# ---- runtime ----
# Distroless: no shell or package manager, runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/devices-api /devices-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/devices-api"]
