# Day 1: Send to Mac

Upload a file from an iPhone (or another device) to your Mac through a browser on the same Wi-Fi. Go standard library only; requires Go 1.24+.

## Run

```sh
cd Day1
mkdir -p received
go run . -dir ./received -port 8080
```

On your iPhone, open the printed Network URL in Safari, including `http://` and the port. Choose a file or photo and tap **Send to Mac**. Wait for **File received**. Find the file in `Day1/received` on the Mac. Each saved filename has a random prefix to prevent overwrites.

Press Ctrl+C to stop. Keep your Mac awake while receiving. If the phone cannot connect, check that both devices are on the same non-guest Wi-Fi and that VPN/firewall settings allow the connection.

## Privacy and limits

This version replaces the original file browser with an upload-only page. There are no directory listings or download routes, even if a visitor knows a filename. The selected directory is only a destination for incoming files.

- One file per request, at most 50 MiB; total request limited to 51 MiB.
- Existing files are never overwritten. Failed writes are removed.
- Uses `os.OpenRoot` to constrain writes to the selected directory.
- Plain HTTP without authentication: anyone who can reach the port can upload and consume disk space. Use only on trusted Wi-Fi and stop the server when finished. Do not forward this port to the internet.
- No Bluetooth or AirDrop integration, automatic file opening, or executable uploads being run.

## Check

```sh
go test ./...
go vet ./...
```

`main.go` reads flags, opens the destination folder, and starts HTTP handlers. The browser sends a multipart form; Go validates its size and saves the file with a unique name.
