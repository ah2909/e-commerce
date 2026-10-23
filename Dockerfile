# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
FROM golang:1.26.10-bookworm@sha256:632dff7351799a5a9954a48dc06670ade72ee44e8c351cdc0e5a57af2fea21af AS toolchain
WORKDIR /src
ENV GOTOOLCHAIN=local

FROM toolchain AS build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/event-reconcile ./cmd/event-reconcile && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/payment-key-rotate ./cmd/payment-key-rotate && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/config-check ./cmd/config-check && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o /out/healthcheck ./cmd/healthcheck

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ /
COPY --from=build /src/migrations /migrations
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/api"]
