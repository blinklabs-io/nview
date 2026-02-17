FROM ghcr.io/blinklabs-io/go:1.25.7-1 AS build

ARG VERSION
ARG COMMIT_HASH
ENV VERSION=${VERSION}
ENV COMMIT_HASH=${COMMIT_HASH}

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS nview
COPY --from=build /code/nview /bin/
ENTRYPOINT ["nview"]
