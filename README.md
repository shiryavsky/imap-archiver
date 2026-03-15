# imap-archiver

A command-line Go tool that connects to any IMAP server and moves old messages
into yearly archive folders, in batches, without downloading message bodies.

WHY: Sometimes your email client can't handle a large number of messages to archive.
This small solution should help. This helped me sort through millions of E-mails over decades.

## Archive layout

```
<archive-root>/
  <YYYY>/
    <source-folder>/   ← mirrors the original folder hierarchy
```

For example, a 2022 message from `Work/Projects` is moved to:
```
Archives/2022/Work/Projects
```

## Quick start

```bash
# 1. Clone / download and build
go mod tidy
go build -o imap-archiver .

# 2. Dry-run — preview what would be archived (nothing is moved)
./imap-archiver \
  --host imap.example.com \
  --user me@example.com \
  --pass secret \
  --folders "INBOX,Sent,Work" \
  --age 365 \
  --dry-run -v

# 3. Run for real
./imap-archiver \
  --host imap.example.com \
  --user me@example.com \
  --pass secret \
  --folders "INBOX,Sent"
```

## Options

| Flag | Default | Description |
|---|---|---|
| `--host` | *(required)* | IMAP server hostname |
| `--user` | *(required)* | IMAP username / email |
| `--pass` | *(required)* | IMAP password (or env `IMAP_PASSWORD`) |
| `--port` | 993 / 143 | IMAP port (auto-detected from TLS mode) |
| `--tls` | `true` | Use implicit TLS (IMAPS) |
| `--starttls` | `false` | Use STARTTLS upgrade |
| `--folders` | `INBOX` | Comma-separated list of source folders |
| `--archive-root` | `Archives` | Root folder for archived mail |
| `--ignore-in-archive` | `INBOX,Sent` | Folder names to strip from archive path |
| `--age` | `365` | Archive messages older than N days |
| `--batch` | `1000` | Max messages per batch move |
| `--dry-run` | `false` | Preview mode — no messages moved |
| `-v` | `false` | Verbose / debug output |
| `-h` | | Show help |

## Archive path filtering

By default, `INBOX` and `Sent` are stripped from the archive path. This means:

- `INBOX/Phabricator` → `Archives/2017/Phabricator`
- `Sent/Marketing` → `Archives/2022/Marketing`

Use `--ignore-in-archive` to customize which folder names are stripped:

```bash
./imap-archiver --host imap.example.com --user me@example.com \
  --pass secret --ignore-in-archive "INBOX,Sent,Archive"
```

## How it works

1. Connects to the IMAP server over TLS (or STARTTLS / plain).
2. For each source folder:
   - Runs `UID SEARCH BEFORE <cutoff>` to find old message UIDs — **no bodies downloaded**.
   - Fetches only `INTERNALDATE` to group UIDs by year.
   - Ensures destination folders exist (`CREATE` if needed).
   - Moves UIDs in batches using `UID MOVE` (RFC 6851) or falls back to `UID COPY` + `STORE \Deleted` + `EXPUNGE` if the server does not support `MOVE`.
3. Prints a summary table.

## Common providers

| Provider | Host | Port | TLS |
|---|---|---|---|
| Gmail | `imap.gmail.com` | 993 | yes |
| Outlook / Hotmail | `outlook.office365.com` | 993 | yes |
| Fastmail | `imap.fastmail.com` | 993 | yes |
| Apple iCloud | `imap.mail.me.com` | 993 | yes |

> **Gmail note:** Enable "Allow less secure apps" or use an App Password when
> 2FA is active. For OAuth2, configure an App Password in your Google Account.

## Dependencies

- [`github.com/emersion/go-imap/v2`](https://github.com/emersion/go-imap) — pure-Go IMAP client

## License

MIT
