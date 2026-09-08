# Security policy

## Supported versions

Security fixes are currently provided for the latest release only.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use GitHub's **Security → Report a vulnerability** flow on this repository and include reproduction steps, affected versions, and any known mitigations. You should receive an acknowledgement within seven days.

## Deployment guidance

Visual Tmux Client provides interactive shell access to the account that runs it. Treat access to the web UI as equivalent to terminal access to that account.

- Keep the default loopback bind unless remote access is necessary.
- Use an HTTPS reverse proxy for remote access; never send the bearer token over plain HTTP.
- Set a strong, stable `VISUAL_TMUX_CLIENT_TOKEN` and store it outside source control.
- Set `VISUAL_TMUX_CLIENT_ORIGIN` to the exact public HTTPS origin.
- Run the service as an unprivileged, dedicated user where practical.
- Rotate a token immediately if it may have appeared in logs, shell history, or source control.

The token is stored in the browser's local storage. Avoid using the UI on shared or untrusted browser profiles.
