ARG BUILD_FROM=ghcr.io/home-assistant/base:latest

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY src/ ./
RUN CGO_ENABLED=0 go build -o /ism7 .

FROM ${BUILD_FROM}
COPY --from=build /ism7 /usr/local/bin/ism7
COPY run.sh /run.sh
RUN chmod +x /run.sh
CMD ["/run.sh"]
