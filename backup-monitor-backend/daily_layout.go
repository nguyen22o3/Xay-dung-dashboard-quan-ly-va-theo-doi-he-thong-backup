package main

// Read both legacy category-first and new date-first storage. Never scan
// backup_restore, aaPanel's unpacked data/ SQL work files, or symlink targets.
const localBackupFindCommand = `find /www/backup -regextype posix-extended -type f \( -regex '/www/backup/((site|database)/.*|20[0-9]{2}-[0-9]{2}-[0-9]{2}/(site|database)/.*)\.(tar\.gz|sql\.gz|sql|zip)' -o -regex '/www/backup/(panel|20[0-9]{2}-[0-9]{2}-[0-9]{2}/panel)/[^/]+\.zip' \)`
