# Day 1: Send to Mac

Receive a file from your iPhone through a browser on the same Wi-Fi. Every file requires approval in your Mac terminal before its contents are uploaded. Go standard library only; requires Go 1.24+.

## Run

```sh
cd Day1
mkdir -p received
go run . -dir ./received
```

Keep the terminal open. It prints a Network URL and a new access code for this session. On your iPhone:

1. Open the Network URL in Safari, including `http://` and `:8080`.
2. Enter the access code from the Mac terminal.
3. Choose one file or photo and tap **Request approval**.
4. On your Mac, review the filename, byte size, and sender's network address. Type the exact `yes ABC123` shown for that request to accept, or `no ABC123` to reject. Each request has a different approval ID.
5. Wait for **File received and saved on your Mac** on the phone. Find it in `Day1/received`.

Only filename and size are sent before approval. File contents are sent afterward using a single-use permission. A rejected or unanswered request saves nothing. If a transfer fails, request fresh approval before retrying. Filenames and sizes are sender-provided descriptions, not a guarantee of the contents or sender's identity.

The server stops automatically after 10 minutes. Press Ctrl+C to stop sooner. Closing the browser does not stop the server. Files already received remain on disk. Received files are excluded from Git.

## Limits and options

```sh
# Stop after the first successful upload:
go run . -dir ./received -once

# Choose another port, session duration, and session byte quota:
go run . -dir ./received -port 8081 -lifetime 5m -quota-mb 100
```

| Protection | Default |
| --- | --- |
| Access code | Random 12-character code, replaced on restart; never put in a URL or browser storage |
| Incorrect-code limit | Five failed attempts trigger a one-minute session-wide lockout |
| Consent | Exact request-specific approval in the terminal; 60 seconds to answer |
| Approval permission | Single-use, valid for 60 seconds; tied to approved name, size, and sender IP address |
| Approval prompt limit | 30 prompts per session, including rejections and timeouts |
| Concurrency | One approval or transfer at a time |
| File size | 50 MiB maximum |
| Session quota | 200 MiB and 20 successful files, including empty files |
| Shutdown | 10 minutes, with up to five seconds to drain connections; optional `-once` |
| Connection timeouts | Five seconds for headers, two minutes to read a request, three minutes to write a response |
| Browser request checks | Exact same-origin requests and a known server address required |
| File access | No listings or downloads; unique names, exclusive creation, owner-only file permissions, writes confined using `os.OpenRoot` |

An approval only works from the same sender IP address; client-supplied forwarding headers are ignored. This is an additional check, not proof of device identity.

Session counters reset on restart; existing files are not included in the quota. Check available disk space yourself. Interrupted or invalid transfers are removed; a process crash can leave a partial file. Files are never automatically opened or executed.

## Network limitations

HTTPS is not implemented. File contents, codes, and upload permissions travel over unencrypted HTTP. Use trusted Wi-Fi only and do not forward this port to the internet. These controls do not make the service suitable for hostile networks; local peers can still cause disruption, including triggering the code lockout.

Keep your Mac awake. Guest Wi-Fi, VPNs, and firewalls can block phone-to-Mac connections. Open a printed address rather than an arbitrary hostname; unknown Host headers are rejected. This is not Bluetooth or AirDrop.

## Code and checks

- `main.go`: flags, terminal consent, network listener, shutdown.
- `receiver.go`: authentication, permissions, quotas, file writes.
- `page.html` and `page.js`: phone upload form and two-step transfer.
- `main_test.go`: consent, privacy, replay, origin checks, quotas, concurrency, and invalid transfers.

```sh
go test -race ./...
go vet ./...
```
