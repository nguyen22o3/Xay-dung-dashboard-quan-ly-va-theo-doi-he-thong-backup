#!/bin/bash
SOURCE_DIR="/etc" 
BACKUP_DIR="/tmp/backups"
API_URL="http://localhost:8080/api/backup"
API_KEY="289a8aa74862ceea693776199597abf5c9736528770f1917920bda69545c3ba6"

DATE=$(date +%Y%m%d_%H%M%S)
FILE_NAME="system_config_$DATE.tar.gz"
DEST_FILE="$BACKUP_DIR/$FILE_NAME"

mkdir -p $BACKUP_DIR
tar -czf $DEST_FILE $SOURCE_DIR > /dev/null 2>&1
EXIT_CODE=$?

if [ $EXIT_CODE -eq 0 ] || [ $EXIT_CODE -eq 1 ]; then
    STATUS="Success"
else
    STATUS="Failed"
fi

if [ -f "$DEST_FILE" ]; then
    SIZE_MB=$(du -m $DEST_FILE | cut -f1)
else
    SIZE_MB=0
fi

curl -s -X POST $API_URL \
-H "Content-Type: application/json" \
-H "X-API-Key: $API_KEY" \
-d "{
  \"source\": \"System Config (/etc)\",
  \"file_name\": \"$FILE_NAME\",
  \"status\": \"$STATUS\",
  \"size_mb\": $SIZE_MB
}" > /dev/null

if [ $EXIT_CODE -eq 0 ] || [ $EXIT_CODE -eq 1 ]; then
    STATUS="Success"
    # Lệnh đẩy file lên Google Drive, lưu vào thư mục "Backup_Dashboard"
    rclone copy $DEST_FILE gdrive:Backup_Dashboard/
else
    STATUS="Failed"
fi
