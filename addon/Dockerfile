ARG BUILD_FROM=ghcr.io/home-assistant/base:latest

FROM golang:1.25-alpine AS build
RUN apk add --no-cache git
# declared before the clone: a version bump in config.yaml invalidates the cached clone
ARG BUILD_VERSION
RUN git clone --depth 1 https://github.com/achmed20/ism7hass /src
WORKDIR /src
RUN CGO_ENABLED=0 go build -o /ism7 .

FROM ${BUILD_FROM}
COPY --from=build /ism7 /usr/local/bin/ism7
COPY run.sh /run.sh
RUN chmod +x /run.sh
CMD ["/run.sh"]
