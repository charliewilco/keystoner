set positional-arguments

export GO := env("GO", "go")

alias up := container-up
alias stop := container-stop

# List available commands.
default:
    @just --list

# Run the public API locally (PORT defaults to 8080).
run:
    "$GO" run ./cmd/keystoner

# Build the API executable.
build:
    CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o keystoner ./cmd/keystoner

# Build the independent discovery CLI.
build-cli:
    mkdir -p work
    CGO_ENABLED=0 "$GO" build -trimpath -ldflags='-s -w' -o work/discover ./cmd/discover

# Discover candidate sources; pass scraper flags after the recipe name.
discover *args:
    "$GO" run ./cmd/discover "$@"

# Format Go source.
fmt:
    "$GO" fmt ./...

# Run static checks.
vet:
    "$GO" vet ./...

# Run Go tests with the race detector.
test-go:
    "$GO" test -race ./...

# Run the catalog publisher tests.
test-publisher:
    python3 -m unittest discover -s scripts -v

# Run all tests.
test: test-go test-publisher

# Run tests, static checks, and shell syntax validation.
check: test vet
    sh -n scripts/container.sh

# Publish a reviewed catalog: just publish RELEASE UTC_TIMESTAMP.
publish release published_at:
    python3 scripts/publish.py --release "$1" --published-at "$2"

# Start Apple's container service (install its kernel first if needed).
container-start:
    container system start

# Build the API image with Apple's container tooling.
container-build:
    ./scripts/container.sh build

# Run the existing API image on localhost.
container-run:
    ./scripts/container.sh run

# Build and run the API image.
container-up: container-build container-run

# Show API container logs.
container-logs:
    ./scripts/container.sh logs

# Stop and remove the API container.
container-stop:
    ./scripts/container.sh stop
