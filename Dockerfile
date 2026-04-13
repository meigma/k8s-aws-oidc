FROM gcr.io/distroless/static-debian12:nonroot@sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1

ARG TARGETPLATFORM

COPY --chmod=0755 $TARGETPLATFORM/oidc-proxy /usr/bin/oidc-proxy

EXPOSE 443 8080

USER 65532:65532

ENTRYPOINT ["/usr/bin/oidc-proxy"]
