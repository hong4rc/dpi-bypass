set windows-shell := ["powershell.exe", "-NoProfile", "-Command"]

binary := if os() == "windows" { "dpi.exe" } else { "dpi-bypass" }
ldflags := if os() == "windows" { "-H=windowsgui -X main.version=dev" } else { "-X main.version=dev" }

# vet + test + build a native binary (no console window on Windows)
build:
    go vet ./...
    go test ./...
    go build -ldflags "{{ldflags}}" -o {{binary}} ./cmd/dpi-bypass

# run from source with console output (Ctrl+C to stop)
run:
    go run ./cmd/dpi-bypass

# restore the system proxy setting if the app was killed
restore:
    go run ./cmd/dpi-bypass -restore
