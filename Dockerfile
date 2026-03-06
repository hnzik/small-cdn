FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/cdn .

FROM alpine:3.21

RUN addgroup -S cdn && adduser -S cdn -G cdn

COPY --from=build /bin/cdn /bin/cdn
COPY config.yaml /etc/cdn/config.yaml

WORKDIR /opt/cdn
RUN mkdir -p cache_data && chown -R cdn:cdn /opt/cdn

ENV GOMEMLIMIT=7GiB

USER cdn
EXPOSE 8080

ENTRYPOINT ["/bin/cdn"]
CMD ["-config", "/etc/cdn/config.yaml"]
