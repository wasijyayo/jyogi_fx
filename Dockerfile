# --- build stage ---
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY backend/go.mod ./backend/go.mod
# go.sum はまだ依存なしのため存在しない。依存追加時に `go mod tidy` して COPY を足すこと。
COPY backend ./backend
WORKDIR /src/backend
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/app ./cmd/app

# --- run stage ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app", "serve"]
