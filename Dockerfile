# Stage 1: The Builder (Strictly locked to amd64)
FROM --platform=linux/amd64 heroiclabs/nakama-pluginbuilder:3.22.0 AS builder

ENV GO111MODULE=on
ENV CGO_ENABLED=1

WORKDIR /backend

# Copy all source code into the container
COPY . .

# Resolve dependencies
RUN go mod tidy

# Compile the plugin with trimpath for extra safety
RUN go build --buildmode=plugin -trimpath -o ./backend.so

# Stage 2: The Final Runtime Image (Strictly locked to amd64)
FROM --platform=linux/amd64 registry.heroiclabs.com/heroiclabs/nakama:3.22.0

# Copy the compiled .so file from the builder stage
COPY --from=builder /backend/backend.so /nakama/data/modules/