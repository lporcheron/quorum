# Generated artifacts are committed, so the image builds with the Go
# toolchain alone: no Node, no templ, no tailwind.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /quorum ./cmd/quorum
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /quorum /quorum
# distroless has no shell to mkdir; ship /data writable by nonroot.
COPY --from=build --chown=nonroot:nonroot /data /data
ENV QUORUM_DB_PATH=/data/quorum.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/quorum"]
