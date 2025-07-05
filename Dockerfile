FROM golang:1.24.4-bookworm AS builder
WORKDIR /app
ADD . /app

# Append a suffix to prevent colliding with the directory of the same name
RUN go build -o geodesist-build

FROM debian:bookworm-slim
COPY --from=builder /app/geodesist-build /app/geodesist

WORKDIR /app
ENTRYPOINT ["/app/geodesist"]