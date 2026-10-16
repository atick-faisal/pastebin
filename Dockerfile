FROM golang:1.27.2 AS build
WORKDIR /src
COPY . .
# A static binary needs no libc, so it runs on the distroless static image.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pastebin ./cmd/pastebin

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /pastebin /pastebin
# Keeps the garbage collector working before the 384 MiB container limit is hit.
ENV GOMEMLIMIT=320MiB
EXPOSE 8080
ENTRYPOINT ["/pastebin"]
