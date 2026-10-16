# Security

## What is public by design

Pastebin has no accounts and no authentication. Anyone with the link can read
and change every text box. Nothing is stored on disk, and a restart wipes all
text. These are intended behaviours, not vulnerabilities. See
[docs/goal.md](docs/goal.md).

## Reporting a vulnerability

Please report problems such as injection, a way to crash or exhaust the server,
or an escape from the text-box model privately, not in a public issue.

Use GitHub's private reporting: open the repository's **Security** tab and choose
**Report a vulnerability**. If that option is not shown, open a public issue that
only says you have a security report and asks for a private way to send it.
Please do not put details in that issue.

I will reply as soon as I can. This is a personal project maintained in spare
time, so there is no fixed response time.

## Supported versions

Only the latest commit on `main` is supported.
