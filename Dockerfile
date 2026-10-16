# syntax=docker/dockerfile:1
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /podport . \
 && mkdir /data && chown 65532:65532 /data

FROM scratch
COPY --from=build /podport /podport
COPY --from=build --chown=65532:65532 /data /data
COPY LICENSE THIRD_PARTY_NOTICES.txt /licenses/
ENV GOMEMLIMIT=48MiB
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD ["/podport", "-healthcheck"]
ENTRYPOINT ["/podport"]
CMD ["-listen", "0.0.0.0:8080", "-db", "/data/podport.db"]
LABEL org.opencontainers.image.title="PodPort" \
      org.opencontainers.image.version="3.0.0" \
      org.opencontainers.image.description="Personal Podman port registry: one static Go binary and SQLite"
