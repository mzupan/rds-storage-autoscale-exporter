# Stage 1: Building the application
FROM golang:1.21 as builder

# Set the working directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o exporter .

# Stage 2: Setup the scratch container
FROM scratch

# Copy the binary from the builder stage
COPY --from=builder /app/exporter /exporter

# Expose the port on which the application will run
EXPOSE 9761

# Run as non-root user for secure environments
USER 59000:59000

# Command to run the executable
ENTRYPOINT ["/exporter"]