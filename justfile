set shell := ["powershell.exe", "-NoProfile", "-Command"]

# vet + build dpi.exe
build:
    go vet ./...
    go build -o dpi.exe .

# build and run (Ctrl+C to stop)
run: build
    ./dpi.exe

# reset Windows proxy setting if dpi.exe was killed
restore:
    ./dpi.exe -restore
