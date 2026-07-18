FROM node:24-alpine AS web-build
WORKDIR /app
ARG VITE_BASE_PATH=/mail/
ENV VITE_BASE_PATH=$VITE_BASE_PATH
COPY package*.json ./
RUN npm ci
COPY index.html vite.config.ts tsconfig.json ./
COPY public ./public
COPY src ./src
RUN npm run build:web

FROM golang:1.26.5-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mail-server ./cmd/mail \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bootstrap-admin ./cmd/bootstrap-admin

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 1000 mailapp \
    && adduser -S -D -H -u 1000 -G mailapp mailapp
WORKDIR /app
ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000 \
    MAIL_WEB_ROOT=/app/dist \
    MAIL_DATA_DIR=/app/data
COPY --from=go-build /out/mail-server /app/mail-server
COPY --from=go-build /out/bootstrap-admin /app/bootstrap-admin
COPY --from=web-build /app/dist /app/dist
RUN mkdir -p /app/data && chown -R mailapp:mailapp /app/data
USER mailapp
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:3000/api/health >/dev/null || exit 1
CMD ["/app/mail-server"]
