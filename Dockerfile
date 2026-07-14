# syntax=docker/dockerfile:1
# Multi-arch build. Go cross-compiles on the native $BUILDPLATFORM for the
# requested $TARGETARCH, so only the tiny distroless runtime layer is emulated.
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/kubernetes-mcp .

FROM gcr.io/distroless/base-debian12:nonroot

COPY --from=builder /out/kubernetes-mcp /kubernetes-mcp

USER nonroot:nonroot
EXPOSE 9090
ENTRYPOINT ["/kubernetes-mcp"]
