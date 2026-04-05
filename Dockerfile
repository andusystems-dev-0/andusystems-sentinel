# Multi-stage build: Go builder + minimal runtime image

# ---- Builder ----------------------------------------------------------------
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o bin/sentinel ./cmd/sentinel

# ---- Runtime ----------------------------------------------------------------
FROM alpine:3.20

# ca-certificates for HTTPS; git for go-git subprocess fallback
RUN apk add --no-cache ca-certificates git

# Non-root user
RUN addgroup -S sentinel && adduser -S sentinel -G sentinel

# [AI_ASSISTANT] Code CLI (installed separately in the registry image, not this base)
# When building the full image, add:
#   COPY --from=[AI_ASSISTANT]-installer /usr/local/bin/[AI_ASSISTANT] /usr/local/bin/[AI_ASSISTANT]

WORKDIR /app

COPY --from=builder /build/bin/sentinel /usr/local/bin/sentinel
COPY prompts/ /app/prompts/

RUN chown -R sentinel:sentinel /app

USER sentinel

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sentinel"]
CMD ["--config", "/etc/sentinel/config.yaml"]
