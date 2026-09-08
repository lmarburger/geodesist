FROM golang:1.27-trixie AS builder
WORKDIR /app
ADD . /app

# Append a suffix to prevent colliding with the directory of the same name
RUN go build -o geodesist-build

FROM debian:trixie-slim
COPY --from=builder /app/geodesist-build /app/geodesist

WORKDIR /app
ENTRYPOINT ["/app/geodesist"]