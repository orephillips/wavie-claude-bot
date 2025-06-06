FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod ./

# Explicitly install dependencies
RUN go mod download github.com/kelseyhightower/envconfig@v1.4.0 && \
    go mod tidy

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
COPY --from=builder /app/docs.zip ./docs.zip
EXPOSE 8080
CMD ["./main"]