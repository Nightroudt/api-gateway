FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /echo-backend ./fixtures/echo-backend

FROM scratch
COPY --from=build /echo-backend /echo-backend
ENTRYPOINT ["/echo-backend"]
