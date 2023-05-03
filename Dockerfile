FROM golang:1.18 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS nview
COPY --from=build /code/nview /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/nview"]
