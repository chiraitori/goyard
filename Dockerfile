FROM golang:1.24-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/goyard ./cmd/goyard
RUN mkdir -p /out/data && chown -R 65532:65532 /out/data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/goyard /goyard
COPY --from=build --chown=65532:65532 /out/data /data
ENV PORT=4001 DB_PATH=/data/goyard.db GOMEMLIMIT=200MiB
VOLUME ["/data"]
EXPOSE 4001
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD ["/goyard", "healthcheck"]
ENTRYPOINT ["/goyard"]
