default:
    @just --list --unsorted

# Run every check.
verify: vet test

# Check Go packages for suspicious code.
vet:
    go vet ./...

# Run tests with data race detection.
test:
    go test -race ./...

# Run the two-browser check; needs Node and Playwright, so it stays out of verify.
browser-check:
    node scripts/browser-check.mjs

# Build the Docker image; needs Docker, so it stays out of verify.
docker-build:
    docker build -t pastebin .

# Run the image on port 8080 under the production memory limit.
docker-run: docker-build
    docker run --rm -p 8080:8080 --memory=384m pastebin
