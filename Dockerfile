FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Cross-compile natively instead of emulating the target architecture.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ocis-dump ./cmd/ocis-dump

FROM gcr.io/distroless/static-debian13
COPY --from=build /out/ocis-dump /usr/local/bin/ocis-dump
ENTRYPOINT ["/usr/local/bin/ocis-dump"]
CMD ["--help"]
