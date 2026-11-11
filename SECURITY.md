# Security

bx gives coding agents control of your real, logged-in browser. Read this
before you install it.

## What bx can do

Through the browser's DevTools Protocol, bx can do anything you can do in
the browser: read any open page, type, click, submit forms, run JavaScript
in a page, and send raw protocol commands with `bx cdp`. It acts with your
logins and cookies.

## What limits it

- **The browser asks first.** Each new DevTools connection makes the browser
  show an Allow dialog. bx holds one connection per browser run, so you
  answer once, after you run a bx command.
- **Local only.** bx listens on no network port. Commands reach its
  background process over a Unix socket in `~/Library/Application Support/bx/`,
  a folder only your user account can open. Any process running as you can
  use that socket, as it could use your browser anyway.
- **No telemetry.** bx sends nothing anywhere. Its only network calls are
  `bx update`, `bx doctor` and a once-a-day release check at a terminal,
  all to GitHub's release API.
- **Verified updates.** `bx update` installs a release only if its SHA-256
  matches the release's `checksums.txt`.
- **No flag injection.** `bx open` and `bx goto` accept only http, https,
  file and about URLs, so a URL can't be read as a browser flag.

## What it doesn't protect against

- **Prompt injection.** An agent that reads a malicious page can be told,
  by text on that page, to act in another tab. bx can't tell your
  instructions from the page's. Give agents narrow tasks, and keep the
  bx skill's rule to ask before submitting, sending, buying or deleting.
- **Your own agent's mistakes.** bx does what it's told. Close tabs you
  don't want touched, or use a browser profile that holds only what the
  task needs.

## Reporting a problem

Report security issues privately through GitHub: open the repo's
**Security** tab and choose **Report a vulnerability**. For anything that
isn't sensitive, open a normal
[issue](https://github.com/Facets-cloud/bx/issues/new).

Only the latest release gets fixes. bx follows SemVer (`0.x.y` for now).
