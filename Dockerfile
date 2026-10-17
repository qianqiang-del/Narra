FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/narra-api ./cmd/server

FROM python:3.13-slim-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libgl1 libglib2.0-0 libgomp1 libxcb1 tzdata \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY pkg/documentparser/python/requirements.txt /app/python/requirements.txt
RUN pip install --no-cache-dir -r /app/python/requirements.txt \
    && python -c "import fitz; from docling.document_converter import DocumentConverter; from rapidocr import RapidOCR"
COPY pkg/documentparser/python/ /app/python/
COPY --from=build /out/narra-api /app/narra-api
COPY configs/config.production.yaml /app/configs/config.production.yaml
RUN mkdir -p /app/data/uploads /app/data/audio /app/data/knowledge
EXPOSE 8080
CMD ["/app/narra-api", "-c", "/app/configs/config.production.yaml"]
