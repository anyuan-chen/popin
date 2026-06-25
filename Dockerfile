FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o popin ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/popin .
VOLUME /app/data
ENV DB_PATH=/app/data/popin.db
EXPOSE 8080
CMD ["./popin"]
