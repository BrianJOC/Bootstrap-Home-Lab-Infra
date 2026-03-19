FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o mock .

FROM alpine:3.19
COPY --from=builder /app/mock /mock
EXPOSE 8080
CMD ["/mock"]
