FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build -trimpath \
  -ldflags="-s -w -X github.com/komari-monitor/komari-agent/update.CurrentVersion=${VERSION}" \
  -o /out/kelicloud-agent .

FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

COPY --from=builder /out/kelicloud-agent /app/kelicloud-agent

RUN chmod +x /app/kelicloud-agent

RUN touch /.kelicloud-agent-container

ENTRYPOINT ["/app/kelicloud-agent"]
# 运行时请指定参数
# Please specify parameters at runtime.
# eg: docker run kelicloud-agent -e example.com -t token
CMD ["--help"]
