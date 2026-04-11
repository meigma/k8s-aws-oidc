FROM gcr.io/distroless/static-debian12:latest

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/oidc-proxy /usr/bin/oidc-proxy

EXPOSE 443 8080

ENTRYPOINT ["/usr/bin/oidc-proxy"]
