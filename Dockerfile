FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /threadline .

FROM golang:1.27.3-alpine AS migration-build
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@v3.28.0

FROM alpine:3.23 AS migrations
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=migration-build /go/bin/goose /usr/local/bin/goose
COPY sql/schema /migrations
USER app
ENTRYPOINT ["/bin/sh", "-c", "exec goose -dir /migrations postgres \"$DB_URL\" up"]

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /threadline /app/threadline
COPY public /app/public
USER app
EXPOSE 8080
CMD ["/app/threadline"]
