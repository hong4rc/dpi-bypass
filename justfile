set shell := ["powershell.exe", "-NoProfile", "-Command"]

# vet + test + build dpi.exe (tray app, no console window)
build:
    go vet ./...
    go test ./...
    go build -ldflags "-H=windowsgui -X main.version=dev" -o dpi.exe ./cmd/dpi-bypass

# run from source with console output (Ctrl+C to stop)
run:
    go run ./cmd/dpi-bypass

# reset Windows proxy setting if dpi.exe was killed
restore:
    go run ./cmd/dpi-bypass -restore
