# Multi-stage build for logsense
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git

# Cache deps first
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE

ENV CGO_ENABLED=0

RUN if [ -z "$DATE" ]; then DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ'); fi; \
    echo "Building logsense ${VERSION} (${COMMIT}) ${DATE}"; \
    go build -ldflags "-s -w -X logsense/internal/version.Version=${VERSION} -X logsense/internal/version.Commit=${COMMIT} -X logsense/internal/version.Date=${DATE}" -o /out/logsense ./cmd/logsense

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/logsense /usr/local/bin/logsense
USER 65532:65532
ENTRYPOINT ["logsense"]
