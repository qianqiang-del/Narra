FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/narra-api ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata curl
WORKDIR /app
COPY --from=build /out/narra-api /app/narra-api
COPY configs/config.production.yaml /app/configs/config.production.yaml
RUN mkdir -p /app/data/uploads /app/data/audio /app/data/knowledge
EXPOSE 8080
CMD ["/app/narra-api", "-c", "/app/configs/config.production.yaml"]
