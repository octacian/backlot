# Build the static fixture binary explicitly with CGO_ENABLED=0 GOOS=linux.
# Backlot never implicitly builds or pulls images.
FROM alpine:3.21
COPY fixture-linux /fixture
ENTRYPOINT ["/fixture"]
