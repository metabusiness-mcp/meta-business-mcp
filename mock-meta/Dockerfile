# Stage 1: Build mock-server
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY main.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o mock-server main.go

# Stage 2: Minimal runtime image
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/mock-server .

EXPOSE 8081

CMD ["./mock-server"]
