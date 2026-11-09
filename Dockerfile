# The build stage runs on the builder's own platform and cross-compiles, so a
# multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build

WORKDIR /src

# Dependencies first so code edits do not invalidate the module cache layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/facilitator ./cmd/facilitator

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/facilitator /facilitator

EXPOSE 4022 9090
USER nonroot:nonroot

# Configuration comes from the environment; mount secrets rather than baking
# private keys into the image.
ENTRYPOINT ["/facilitator"]
