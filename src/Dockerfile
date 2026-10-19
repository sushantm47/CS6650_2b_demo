# ---------- Stage 1: build ----------
FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /product-service

# ---------- Stage 2: run ----------
FROM alpine:3.22
COPY --from=build /product-service /product-service
EXPOSE 8080
ENTRYPOINT ["/product-service"]
