# build backend
FROM golang:1.23.4-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server main.go

# build container
FROM alpine:3.14

WORKDIR /app

COPY --from=builder /app/server .

RUN apk add --no-cache ca-certificates

EXPOSE 8080

CMD ["./server"]