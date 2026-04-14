FROM golang:1.26.2 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -buildid=" -o /out/oidc-proxy ./cmd/oidc-proxy

FROM gcr.io/distroless/static-debian12:nonroot@sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1

COPY --chmod=0755 --from=build /out/oidc-proxy /usr/bin/oidc-proxy

EXPOSE 443 8080

USER 65532:65532

ENTRYPOINT ["/usr/bin/oidc-proxy"]
