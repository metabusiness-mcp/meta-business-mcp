# Stage 1: Build dashboard
FROM node:20-alpine AS dashboard-builder
WORKDIR /app/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm install
COPY dashboard/ ./
RUN npm run build

# Stage 2: Build Go application
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copy dashboard build output for go:embed
COPY --from=dashboard-builder /app/dashboard/out ./cmd/server/dashboard_out
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server/main.go

# Stage 3: Runtime image
FROM alpine:latest
RUN apk --no-cache add ca-certificates curl
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/policies.yaml .
EXPOSE 8080
CMD ["./server"]
