# Day 1: Local file sharing

Share a folder with another device on your Wi-Fi using a browser. Built with Go's standard library.

## Run

Requires Go 1.24 or newer.

```sh
cd Day1
go run . -dir "$HOME/Downloads" -port 8080
```

Prefer a dedicated folder containing only the files you want to share. Open one of the printed **Network** URLs on your other device. Both devices must be able to reach each other on the network; guest Wi-Fi, VPNs, or a firewall can prevent this. If multiple addresses appear, use the one belonging to your Wi-Fi connection.

Browse folders and click a file to open it. To save a file your browser previews, use **Save As** or **Download Linked File**. Press **Ctrl+C** in the terminal to stop sharing. If port 8080 is busy, choose another port with `-port`.

## Scope

- Read-only: GET and HEAD requests; no uploads or deletion.
- Shares all contents of the chosen folder, including hidden files. A folder's `index.html`, if present, is displayed instead of its directory listing.
- Symlinks cannot expose files outside the chosen folder, enforced with `os.OpenRoot`.
- Plain HTTP, no passwords or encryption. Intended for trusted local networks. Anyone who can reach the listening port can read the shared files. Do not forward the port on your router.
- Listens on IPv4 interfaces. Shared HTML is sandboxed, so this is a file-sharing tool rather than an application host.

## How it works

`flag` reads the folder and port. `os.OpenRoot` establishes the shared filesystem boundary. `http.FileServerFS` handles listings and file responses, including byte ranges. A small handler restricts requests to reading files.

## Check

```sh
go test ./...
go vet ./...
```
