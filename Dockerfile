FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /marco .

FROM alpine:3.22
# iproute2 gives marco `ip neigh`, which reports whether ARP answers are fresh.
RUN apk add --no-cache iproute2 ca-certificates tzdata
COPY --from=build /marco /usr/local/bin/marco
VOLUME /data
ENV MARCO_DB=/data/marco.db MARCO_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/marco"]
