# CLAUDE.md §3: フロントエンドを Go の embed でバイナリに同梱した「単一バイナリ」を作る。
# そのため Node でSPAをビルド → その成果物を Go のビルドステージへ渡す、という2段構えにする。

# --- frontend build stage ---
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
# 依存定義だけを先に COPY することで、ソース変更のたびに npm ci が走るのを防ぐ（レイヤーキャッシュ）。
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
# orval 生成物（src/api/generated/）は .gitignore 対象だが、
# openapi.yaml から `npm run gen` で再生成できる。生成には api/openapi.yaml が要る。
COPY api ../api
COPY frontend ./
RUN npm run gen && npm run build
# vite.config.ts の outDir 設定により、成果物は /src/backend/web/ に出力される。

# --- backend build stage ---
FROM golang:1.25-alpine AS build
WORKDIR /src
# go.mod / go.sum を先に COPY して依存をダウンロードしておく（レイヤーキャッシュ）。
COPY backend/go.mod backend/go.sum ./backend/
WORKDIR /src/backend
RUN go mod download
WORKDIR /src
COPY backend ./backend
# フロントエンドのビルド成果物を embed 対象の位置に配置する。
# これが無いと backend/web/ が .gitkeep だけになり、SPAが配信されない。
COPY --from=frontend /src/backend/web ./backend/web
WORKDIR /src/backend
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/app ./cmd/app

# --- run stage ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app", "serve"]
