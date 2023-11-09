# syntax=docker/dockerfile:1.6

ARG GOLANG_VERSION="1.21"
ARG ALPINE_VERSION="3.18"

# =============================================================================
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder

SHELL ["/bin/ash", "-e", "-u", "-o", "pipefail", "-o", "errexit", "-o", "nounset", "-c"]

RUN apk --no-cache add bash make gawk git

SHELL ["/bin/bash", "-e", "-u", "-o", "pipefail", "-o", "errexit", "-o", "nounset", "-c"]

COPY . /go/src/github.com/rclone/rclone/
WORKDIR /go/src/github.com/rclone/rclone/

ENV GO111MODULE="on"
ENV CGO_ENABLED="0"
RUN make

RUN ./rclone version

# =============================================================================
# Begin final image
FROM alpine:${ALPINE_VERSION} as release

SHELL ["/bin/ash", "-e", "-u", "-o", "pipefail", "-o", "errexit", "-o", "nounset", "-c"]

RUN apk --no-cache add ca-certificates fuse3 tzdata && \
  echo "user_allow_other" >> /etc/fuse.conf

COPY --from=builder /go/src/github.com/rclone/rclone/rclone /rclone

ENTRYPOINT [ "/rclone" ]

WORKDIR /data
ENV XDG_CONFIG_HOME=/config
