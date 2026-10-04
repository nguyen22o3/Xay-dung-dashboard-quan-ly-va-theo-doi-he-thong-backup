# Go backup operations on AlmaLinux

The five former Python ops files are replaced by Go production code in
`internal/backupops`, the Linux CLI in `cmd/backup-manager`, and Go tests.
Windows runs the HTTP backend; it sends a JSON request over SSH to
`/usr/local/libexec/backup-monitor/backup-manager config` on AlmaLinux.
Shell backup/cron scripts call the same binary's `layout` command. The binary
path is stable even when the configurable script/log/storage paths change.
The dashboard API and settings JSON keep their existing field names.
Python used internally by aaPanel is unrelated and must not be uninstalled.

## Build and install

From the backend directory on Windows PowerShell:

```powershell
go test ./...
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -trimpath -o backup-manager-linux-amd64 ./cmd/backup-manager
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

Deploy the executable to the stable path above as root, mode `0700`, through
a verified temporary file and atomic rename. On Linux, build with
`CGO_ENABLED=0 go build -trimpath -o backup-manager ./cmd/backup-manager`.
Check `uname -m` before selecting `amd64` versus `arm64`.

After deploying the binary, inspect `backup-manager install` first. Only
`backup-manager install --apply` updates the four known managed scripts;
it validates Bash syntax, rejects symlinks/active job locks, and saves private
originals under `/root/backup-layout-migrations/before-go-helper-*`.
It never generates, moves, uploads or deletes backup data and never edits cron.
If the scripts already use Go, installation is a no-op.
The retired Python sources are recoverable locally at
`D:\DATN\.backup-monitor-runtime\python-before-go-20261002`.

## Date-first backup layout

Canonical archives on AlmaLinux:

```text
/www/backup/YYYY-MM-DD/site/<existing-relative-path>
/www/backup/YYYY-MM-DD/database/<existing-relative-path>
/www/backup/YYYY-MM-DD/panel/<completed-ZIP>
```

Dates come from archive names, not modification times or the migration date.
Website and database scripts write directly to their dated directory. Relative
paths such as `mysql/crontab_backup/sql_web1_local` remain unchanged to match
existing Google Drive copies. aaPanel continues to use `/www/backup/panel` as
its work area; completed ZIPs are collected by panel cleanup and Drive upload.
Unpacked SQL work files and `backup_restore` are never migrated.

`backup-manager layout organize` is preview-only. Add `--apply` to move completed
archives without overwriting destinations. Every transfer batch records the old
and new paths, sizes, original mtimes and SHA-256 in root-only
`/root/backup-layout-migrations/moves-*.json`. The original archive is unlinked
only after an exclusive hard link and checksum validation. Same-day aaPanel ZIP
replacements are assigned distinct version names. A database/site name collision
aborts preflight without moving anything.

`backup-manager layout prune site|database|panel` previews expiration;
only `--delete` deletes files.
Retention is 14 distinct backup dates per category. Tagged versions retain
**1 cron + 2 manual versions per target/day**; untagged historical files remain
subject only to the original 14-date expiration. Future-dated files are excluded
from expiration. Script-controlled
transfers/deletions create path-specific short-lived rotation markers so the
existing deletion monitor does not report them as unexpected loss.

Drive upload uses `rclone copy`, never `sync`, across locally retained date
directories. This includes manual backups from a previous day whose daily upload
already ran. After a successful upload, `rclone check --one-way` verifies the
retained files before explicitly tagged obsolete Drive versions are deleted.
It never uses `sync` to mirror arbitrary missing files. The original 14-date
expiration still applies. Migration itself does not upload or delete backups.

## Daily version limits

`backup-manager versions install --preview` checks the four managed script
patches without writing. `versions install --apply` saves private originals and
updates scripts after Bash validation, with rollback on failure. It does not
create or remove archives or change cron. Deploy the backend-generated scheduled
wrapper and enable scheduled tracking for website/database/panel too: it exports
`BACKUP_MONITOR_ORIGIN=cron`. Dashboard manual runs export `manual`; direct shell
runs default to `manual`. All schedules keep their original times/arguments.

New file names contain `_cron_<random-id>_` or `_manual_<random-id>_`.
`versions commit <category> <archive>` validates the new completed compressed
archive before keeping one cron or the latest two manual copies in that same
directory/target/day. Site/SQL archives use unique nonces too, so multiple runs
within one second do not overwrite one another. A manual run never rotates a
cron copy. Failed creation or validation leaves prior copies untouched.
Expected rotations write the existing inotify suppression markers and `ROTATED`
log lines. A damaged retained copy blocks rotation rather than risking recovery.

`versions drive YYYY-MM-DD` previews eligible cloud rotations; `--apply` performs
them only after successful upload verification. Only complete local 1-cron or
2-manual groups can authorize removal of explicitly tagged extras from the same
Drive group. Untagged files, other targets and independent cloud copies with an
incomplete local group are preserved. Old unclassified archives are NOT relabeled
or retroactively deleted based on their hour. Native aaPanel backups stay enabled
and unclassified; this limit governs the dashboard-managed scripts.

`backup-manager audit` reads archive counts, bytes, SHA-256, inode and mtime
without modifying them. `backup-manager verify-manifest /path/to/moves.json`
checks archived files against both old Python and new Go migration manifests.
Do not verify an expired migration after intentional retention has removed its
archives. Migration/cleanup/upload are separate explicit operations, never
part of converting the helper implementation.

Recovery requires stopping the relevant backup jobs, validating checksums in the
transfer manifest, moving each destination back to its original source without
overwriting any newly-created file, and restoring the saved scripts. Do not
blindly replace files if jobs have generated more archives since migration.

Tests:

```text
go test ./...
go vet ./...
```

Remote diagnostic/install tests are opt-in, disabled during normal `go test`:
`BACKUP_MONITOR_REMOTE_DIAGNOSTICS=1` enables read-only SSH/API diagnostics.
`BACKUP_MONITOR_GO_OPS_TESTS=1` runs the cross-built Linux unit-test executable
in a private temporary directory on AlmaLinux (fake cron/Drive, fixture files).
For this development workspace only, additionally setting
`BACKUP_MONITOR_DEPLOY_GO_OPS=1` enables the explicit binary/helper install
test. It compares before/after archive hashes and metadata, config and crontab;
all layout/prune commands in that deployment test are previews, not mutations.

## Backup system configuration

### Short archive filenames

Managed archives now omit `_cron_<nonce>` and `_manual_<nonce>` from their
published filenames, for example `web_web1.local_20261004_050001_site.tar.gz`,
`db_sql_web1_local_20261004_043001_mysql_data.sql.gz`, and `2026-10-04_051002.zip`.
Same-second collisions get `_2`, `_3`, etc.; existing files are never overwritten.
Cron/manual origin is recorded in each day's `.backup-monitor-versions.json`,
which is copied to Drive alongside archives. Preserve this metadata when moving
backups: it maintains the separate 1-cron / 2-manual pools. Legacy unclassified
files are never classified by their clock time.

`backup-manager versions names --preview` inventories explicitly tagged files
in configured local storage and Drive without changing them. `--apply` acquires
the managed-job locks, renames them without replacement, verifies local SHA-256
and Drive file IDs/hashes, and patches the four backup/sync scripts. It does not
run a backup, prune, or rotate. Original scripts, original metadata, and the rename
manifest are retained in `/root/backup-layout-migrations/before-clean-names-*`.
On failure it attempts to restore archive names and scripts; metadata may retain
unused origin entries, which do not trigger deletion without verified archives.

### Source data selection

The unified form replaces the Drive-connection row with a per-task source folder
and explicit scope. The existing rclone remote is preserved. Preview/apply saves
`siteSource`, `databaseSource` or `panelSource` alongside paths and the selected
schedule, then patches the matching managed script atomically. A failed save
restores script/cron originals. Source data and existing archives are not moved
or deleted; scripts use the new source on subsequent cron/manual executions.

For websites, `folder` archives the chosen directory; `children` creates a
separate archive per first-level child, excluding `default` and symlinks. For
databases, choose the MySQL data root with `children`, or one immediate user
database subfolder with `folder`. This selects the database for **mysqldump +
gzip**, never a tar archive of live database files. aaPanel sources must contain
`data`, `config`, and `vhost`; its SQLite snapshots and ZIP process stay intact.

Sources must already exist, have no
symlink path components, and remain separate from backup/script/log storage.
The source picker is read-only and cannot create folders. Credentials, dump
options, backup publication, and the 14-date / 1-cron + 2-manual policy stay intact.
Existing configuration without source fields retains its previous scripts until
the user explicitly previews and confirms a source change.

Settings → Backup system manages paths and the five existing cron schedules.
The authoritative root-only JSON is `/root/backup-monitor/system-config.json`.
Without that file, the existing `/www/backup`, `/root/scripts`, `/root` log
directory, and `gdrive:Backup` are used. Retention stays at 14 backup dates;
this screen does not alter that policy or remove intraday versions.

The local picker browses the real directory tree from `/`, independently of
task type (which selects the execution script). Hidden directories are included;
symlink directories are not traversed. Browsing is read-only; creation requires
an explicit click. Local selection can use custom absolute ASCII paths anywhere
on the server, without symlinks. `/` and `/proc`, `/sys`, `/dev`, `/run` trees
cannot be saved as a source or destination. Sources and backup storage must not
contain one another. Script/log directories must be root-owned and not writable by other
users. Drive selection is restricted to existing rclone Google Drive remotes;
credentials never appear in API responses. Arbitrary commands are not accepted.

Preview validates managed scripts, destination collisions, data hashes, and
the current crontab without writing anything. Apply requires that preview's
token and configuration version, acquires the same job/crontab locks, and
refuses busy jobs. Local date directories move only within one filesystem,
preserving names, bytes, and mtimes. Scripts and logs are updated/copied,
unrelated crontab entries remain unchanged, and the central JSON is committed
last. Failures roll back the moved directories and updated scripts/crontab.
Recovery copies are saved under `/root/backup-layout-migrations/system-config-go-*`.

Changing Drive destination does NOT move/delete old Drive backups or run an
upload; future uploads use the new destination and the dashboard reads it.
Changing local storage leaves native aaPanel staging in `/www/backup`;
the updated daily helper collects completed archives from that original root.
Never replace that helper with a pre-configuration version after changing paths.

The isolated configuration/layout/installer tests live in
`internal/backupops/*_test.go`; the normal Windows test run does not SSH or
touch production storage. Linux-only root-ownership/flock/symlink behavior
is also exercised on AlmaLinux using the opt-in Linux unit-test executable.
