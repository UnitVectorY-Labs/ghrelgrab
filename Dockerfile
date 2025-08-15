# Use the official Golang image for building the application
FROM golang:1.24.6 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go modules manifest and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code into the container
COPY . .

# Ensures a statically linked binary
ENV CGO_ENABLED=0

# Build the Go server
RUN go build -mod=readonly -o ghrelgrab .

# Use a minimal base image for running the compiled binary
FROM gcr.io/distroless/base-debian12

# Copy the built server binary into the runtime container
COPY --from=builder /app/ghrelgrab /ghrelgrab
