# syntax=docker/dockerfile:1
# Monólito Kurz: build estático e imagem distroless, sem shell, usuário não root.
FROM golang:1.26.10-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kurz ./cmd/kurz \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kurz-loadtest ./loadtest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kurz /kurz
COPY --from=build /out/kurz-loadtest /kurz-loadtest
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kurz"]
