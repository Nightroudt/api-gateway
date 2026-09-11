FROM golang:1.26-alpine AS build
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gateway ./cmd/gateway

FROM scratch
# Needed for TLS verification when BACKEND_TASKS_URL/BACKEND_SALES_URL point
# at real https:// services (e.g. the actual portfolio services in
# production) — without this, cert verification fails with
# "x509: certificate signed by unknown authority".
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /gateway /gateway
EXPOSE 8080
ENTRYPOINT ["/gateway"]
