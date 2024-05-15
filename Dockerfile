FROM ghcr.io/blinklabs-io/go:1.21.10-1 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS nview
COPY --from=build /code/nview /bin/
ENTRYPOINT ["nview"]
