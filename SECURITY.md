# Security policy

## Supported versions

Security fixes are currently provided for the latest release only.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use GitHub's **Security → Report a vulnerability** flow on this repository and include reproduction steps, affected versions, and any known mitigations. You should receive an acknowledgement within seven days.

## Deployment guidance

Visual Tmux Client provides interactive shell access to the account that runs it and any remote hosts configured in its registry. Treat access to the web UI as equivalent to terminal access to those accounts.

- Keep the default loopback bind unless remote access is necessary.
- Use an HTTPS reverse proxy for remote access; never send the bearer token or credentials over plain HTTP.
- Set a strong, stable `VISUAL_TMUX_CLIENT_TOKEN` and store it outside source control.
- Set `VISUAL_TMUX_CLIENT_ORIGIN` to the exact public HTTPS origin.
- Run the service as an unprivileged, dedicated user where practical.
- Rotate a token immediately if it may have appeared in logs, shell history, or source control.

The token is stored in the browser's local storage. Avoid using the UI on shared or untrusted browser profiles.

## Multi-Host Security Architecture

### Single-Admin Authority and Tenant Isolation

- **Centralized Authentication**: Visual Tmux Client operates under a single administrative authority. The central bearer token grants full administrative authority to configure remote hosts and open terminal sessions.
- **Credential Segregation**: Remote hosts authenticate using their own independent SSH credentials (passwords or private keys). Central login bearer tokens are never forwarded to or shared with remote hosts.
- **Independent Failure Boundaries**: If an SSH connection or authentication fails on a remote host, the failure is reported within that host's workspace; it never terminates the central login session or locks out other hosts.

### Server Trust and Host Key Pinning

- **Trust on First Use (TOFU) with Admin Confirmation**: Before credentials are exchanged with a new remote host, the Hub probes the remote endpoint and presents its cryptographic host key fingerprint (SHA-256) and key type (e.g. `ssh-ed25519`) to the administrator for explicit approval.
- **Cryptographic Challenges**: Enrollment uses short-lived (30s), endpoint-bound challenge tokens. Credentials are never sent to an unverified endpoint.
- **Pinned Host Keys**: Once confirmed, host keys are pinned in the local database. If a remote host's key changes (e.g., due to machine replacement or man-in-the-middle tampering), connection attempts are refused immediately until explicitly verified and updated by the administrator.

### Credential Encryption at Rest

- **Authenticated Encryption (AES-256-GCM)**: All stored passwords, private keys, and passphrases are encrypted at rest using AES-256-GCM.
- **Master Key Persistence**: An independently persisted 32-byte master key (`master.key`) is generated with strict private permissions (`0600`).
- **Cryptographic Binding (AAD)**: Additional Authenticated Data (AAD) binds each encrypted credential to its specific host ID, credential type, and envelope version, preventing cross-host cipher manipulation or replay.
- **Redaction**: Stored secrets are never serialized into API responses, browser local storage, or application logs.

### Persistent Mounts in Container Deployments

When running in containerized environments (such as Docker or Kubernetes):
- The `state_dir` (by default `~/.config/visual-tmux-client` containing `hosts.db` and `master.key`) **must be mounted to a persistent volume**.
- If container storage is ephemeral and the volume is lost, the master key and host registry will be recreated from scratch on restart, requiring re-enrollment of remote servers.

### Backup, Recovery, and Rollback

1. **Backup**:
   - To back up the host registry, copy both `hosts.db` and `master.key` from `state_dir`.
   - SQLite uses write-ahead logging (WAL); ensure `hosts.db-wal` and `hosts.db-shm` (if present) are captured, or perform backups when the service is stopped.
2. **Recovery**:
   - Restore `hosts.db` and `master.key` together to `state_dir` with `0600` permissions on `master.key`.
   - If the master key is missing or corrupted, the service refuses to start and will **never** overwrite or wipe existing host records without valid authorization.
3. **Rollback**:
   - If rolling back to an earlier backup or single-host version, restore the matching `hosts.db` and `master.key` backup.
   - If reverting to local-only operation without multi-host support, remove or comment out `hosts:` in your configuration YAML; local tmux operation requires no database or master key.

### Remote File Semantics and Limitations

- **Account-Bound SFTP**: Remote file operations execute through an SFTP subsystem bound to the remote SSH user account. Remote operations are bounded by that account's remote POSIX permissions, not root-handle confinement.
- **Atomic Replacement**: Overwriting remote files requires the OpenSSH `posix-rename@openssh.com` SFTP extension to guarantee atomic replacement. On remote servers lacking this capability, overwrite is disabled in the web UI (browsing, preview, and downloading remain fully functional).
- **Hard-Link Semantics**: Remote atomic replacement uses rename-over-target semantics. Saving an edited file replaces that specific entry; other hard-link names pointing to the original inode retain their previous contents. Users are explicitly prompted to acknowledge this behavior.
