# Stage 1
FROM golang:1.26.4 AS builder

WORKDIR /app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /app/homestead

# Stage 2
FROM gcr.io/distroless/static-debian12

COPY --from=builder /app/homestead /homestead

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/homestead"]