FROM golang:1.24-alpine AS build

WORKDIR /src

# Dependencies first so code edits do not invalidate the module cache layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/facilitator ./cmd/facilitator

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/facilitator /facilitator

EXPOSE 4022 9090
USER nonroot:nonroot

# Configuration comes from the environment; mount secrets rather than baking
# private keys into the image.
ENTRYPOINT ["/facilitator"]
