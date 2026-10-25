# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO disabled → fully static binary, runs in a distroless/static image.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/delete-watched-media .

# Runtime stage: tiny, no shell, runs as non-root
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/delete-watched-media /delete-watched-media
ENTRYPOINT ["/delete-watched-media"]
CMD ["--config", "/config.yaml"]
