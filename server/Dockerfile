# syntax=docker/dockerfile:1
# Monólito Kurz: build estático e imagem distroless, sem shell, usuário não root.
# Compila sempre na arquitetura de quem builda e cruza para a do destino (TARGETARCH): `--platform linux/amd64`
# num Mac arm64 gera a imagem do pizito sem emulação e sem `exec format error`.
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/kurz ./cmd/kurz \
 && CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/kurz-loadtest ./loadtest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kurz /kurz
COPY --from=build /out/kurz-loadtest /kurz-loadtest
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kurz"]
