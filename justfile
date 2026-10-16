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
