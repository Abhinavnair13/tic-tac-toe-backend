
FROM heroiclabs/nakama-pluginbuilder:3.22.0 AS builder

ENV GO111MODULE=on
ENV CGO_ENABLED=1

WORKDIR /backend

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build --buildmode=plugin -o ./backend.so

FROM registry.heroiclabs.com/heroiclabs/nakama:3.22.0

# Copy the compiled .so file from the builder stage into Nakama's module directory
COPY --from=builder /backend/backend.so /nakama/data/modules/

