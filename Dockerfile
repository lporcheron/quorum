# Generated artifacts are committed, so the image builds with the Go
# toolchain alone: no Node, no templ, no tailwind. The build stage runs
# on the build platform and cross-compiles for the target, so
# multi-arch releases never emulate the compiler.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /quorum ./cmd/quorum
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /quorum /quorum
# distroless has no shell to mkdir; ship /data writable by nonroot.
COPY --from=build --chown=nonroot:nonroot /data /data
ENV QUORUM_DB_PATH=/data/quorum.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/quorum"]
