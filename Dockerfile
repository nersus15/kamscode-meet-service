# Satu Dockerfile untuk kedua binary:  docker build --build-arg APP=api|sfu -t kamscode-meet-<app> .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP
RUN test -n "$APP" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./apps/${APP}

# distroless: tanpa shell/paket, berjalan sebagai non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot
ENTRYPOINT ["/app"]
