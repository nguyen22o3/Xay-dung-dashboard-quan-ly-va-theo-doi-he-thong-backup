# Thư mục ứng dụng trên AlmaLinux (helper v26)

Bộ cài hỏi một thư mục chung, mặc định `/opt/backup-monitor`. Có thể truyền
`--data-dir /data/backup-monitor` nếu thư mục cha đã tồn tại và do root quản lý.

```text
/opt/backup-monitor/
  config/           system-config.json, cấu hình Drive, metadata
    rclone/
    mysql.cnf       bản sao .my.cnf cũ nếu có; root, 0600
  scripts/          script mặc định, script giám sát, script riêng của cron
  logs/             log mặc định và các cron, lịch sử chạy
  recovery/         dữ liệu phục hồi, rollback và crontab trước chuyển
  tmp/              vùng làm việc tạm của backup aaPanel
```

Các cron kiểu cũ có thư mục `cron-<id>` giữ cấu trúc đó; cron dùng tên file riêng
giữ các tên `backup-database-<id>.sh`, `backup-monitor-run-<id>.sh`, v.v.
Không đổi lịch chạy, nguồn dữ liệu, nơi chứa các bản backup hoặc tài khoản Drive.

Một locator root-owned, quyền 0600 ở `/etc/backup-monitor/installation.json`
giúp cron, helper và backend tìm cùng một cấu hình. Helper vẫn ở
`/usr/local/libexec/backup-monitor/backup-manager`; khóa chạy ở `/run`. Đây là
các điểm tích hợp hệ thống, không phải dữ liệu lưu trong `/root`.

## Máy hiện tại: frontend/backend Windows, SSH đến AlmaLinux

Chuẩn bị cùng một thư mục gói gồm:

- `backup-manager-linux-v26` (Linux x86_64)
- `backup-manager-linux-v26.sha256` (một dòng SHA256 và tên binary)
- `install-helper-v26.sh` (bản sao `ops/install-storage.sh`)

Dừng backend Windows bằng Ctrl+C. Chép gói lên một thư mục riêng do root quản lý
trên AlmaLinux (không cần dùng `/root`), rồi chạy:

```bash
bash /opt/backup-monitor-v26-package/install-helper-v26.sh
# Hoặc chỉ định vị trí:
bash /opt/backup-monitor-v26-package/install-helper-v26.sh --data-dir /data/backup-monitor
```

Bộ cài kiểm tra checksum/version, xem trước các file sẽ sao chép, yêu cầu xác nhận,
tạm dừng crond/host agent nếu đang hoạt động, rồi chuyển cấu hình. Nếu có tác vụ
backup/khôi phục đang chạy, dừng trước khi chuyển dữ liệu thay vì ngắt tác vụ.
Bộ cài không chạy backup, đồng bộ hoặc dọn dữ liệu. Nếu API Linux native đang
chạy, phải dừng và cập nhật đường dẫn service/env riêng trước khi dùng lại.

Khởi động lại backend Windows (`go run ./cmd/api`) rồi làm mới dashboard. Backend
nhận vị trí cài đặt từ helper; không cần sửa đường dẫn Linux trong Windows `.env`.

## Cài mới trên AlmaLinux bằng Docker

`ops/install-local.sh` hỗ trợ `--data-dir` và cùng bước xác nhận. Phần triển khai
Compose ở `<data-dir>/deployment`. Secret/socket host agent vẫn ở thư mục hệ
thống `/var/lib/backup-monitor-agent`; dữ liệu tài khoản API vẫn trong Docker
volume hiện có. Không đổi các vị trí này âm thầm khi nâng cấp.

Bộ cài này yêu cầu image chứa helper v26 (mặc định release dự kiến 1.1.6).
Image đã phát hành 1.1.5 không hỗ trợ tính năng này. Chưa có thao tác publish
Docker Hub trong thay đổi này; chỉ dùng khi cả ba image của release mới đã có.

## An toàn và cập nhật tiếp theo

- Sao chép, kiểm tra checksum và cú pháp Bash trước khi đổi cron/locator.
- Giữ nguyên toàn bộ file gốc. Lỗi trước khi commit không mất cấu hình cũ;
  lỗi ghi locator khôi phục crontab cũ. Bản sao thất bại được giữ nếu đã công bố
  thư mục đích; bộ cài không trộn hoặc ghi đè thư mục đó khi thử lại.
- Bản SQL/website/aaPanel và dữ liệu Drive không được chuyển bởi thao tác này.
- Không nhận thư mục nằm trong `/root`, hệ thống tạm, website/database/panel,
  nguồn backup hoặc nơi chứa backup. Không nhận symlink hoặc thư mục cha cho
  người khác ghi. Thư mục đích phải chưa tồn tại khi chuyển lần đầu.
- Khi cập nhật, bộ cài tự dùng lại thư mục đã lưu. Không tự chuyển thư mục ứng
  dụng đã cài sang một thư mục khác; thao tác đó cần một luồng migration riêng.
- Không chạy lại installer helper v20–v25 sau khi đã chuyển: helper cũ không biết
  locator v26. Cũng không xóa sạch `/root`: SSH, cấu hình shell, MySQL/aaPanel
  dùng chung và dữ liệu ngoài phạm vi ứng dụng có thể vẫn cần giữ.
- Các thư mục staging/bộ cài cũ trong `/root` không được tự dọn. Kiểm tra và dọn
  riêng sau khi đã xác nhận cron và dashboard hoạt động đúng.
