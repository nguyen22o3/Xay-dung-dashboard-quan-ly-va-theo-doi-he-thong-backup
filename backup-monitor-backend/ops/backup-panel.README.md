# Script sao lưu cấu hình aaPanel

`backup-panel.sh` là script Bash độc lập, không dùng Python. Script sao lưu
`data`, `config`, `vhost` dưới `/www/server/panel`; không sao lưu toàn bộ máy chủ,
mã nguồn website hay nội dung database MySQL. Hai cron database/site hiện tại
vẫn đảm nhiệm những dữ liệu đó.

Script đã được thêm vào Cronjob dashboard với lịch 05:10 hằng ngày theo giờ
máy chủ. Không tự tắt hoặc thay thế tác vụ tự sao lưu nội bộ aaPanel.

## Đầu ra và cấu hình

- Đọc `backupRoot` và `logsDir` từ helper Go đã cài; không cố định đường dẫn
  lưu trữ khi người dùng đã đổi cấu hình trên dashboard.
- Lưu `<backupRoot>/YYYY-MM-DD/panel/YYYY-MM-DD_HHMMSS_<cron|manual>_<random>.zip`.
- Log tại `<logsDir>/backup-panel.log`, gồm kết quả, đường dẫn và thời lượng.
- Tên có mã riêng nên các lần chạy trong cùng một giây không ghi đè nhau.
- Không upload hoặc dọn các ngày hết hạn. Cron Drive và dọn panel vẫn áp dụng
  chính sách 14 ngày có backup; script chỉ xoay vòng bản cùng ngày có nhãn.
- `LOG_FILE`, `PANEL_ROOT`, `LOCK_ROOT` có thể ghi đè rõ ràng qua môi trường;
  mặc định khóa ở `/run`. Khi chạy thật, `BACKUP_ROOT` phải khớp cấu hình Go
  để helper xác minh và xoay vòng đúng thư mục được quản lý.

Bản mới có nhãn `cron` hoặc `manual`: mỗi ngày giữ 1 bản cron và 2 bản thủ công
mới nhất. Chỉ xóa bản thủ công cũ nhất sau khi bản mới qua kiểm tra ZIP; bản
cron không bị lần chạy thủ công xóa. Chạy trực tiếp script mặc định là thủ công;
lịch dashboard truyền `BACKUP_MONITOR_ORIGIN=cron`. Tệp cũ chưa có nhãn và bản
native aaPanel được giữ nguyên, không tự suy đoán loại dựa trên giờ tạo.

## Cách chạy sau khi tự tải file lên AlmaLinux

Ví dụ **nếu** bạn đã đặt file ở `/root/scripts/backup-panel.sh`:

```bash
chmod 700 /root/scripts/backup-panel.sh
bash /root/scripts/backup-panel.sh --dry-run
# Lệnh bên dưới tạo một backup thật; không chạy trong quá trình kiểm thử.
bash /root/scripts/backup-panel.sh --run
```

Không có tham số cũng tương đương `--run`. Không thêm cron mới trước khi chọn
lịch phù hợp với việc đẩy Drive. Script không tự tắt cơ chế backup aaPanel;
nếu chạy cả hai thì có thể có thêm các bản sao cấu hình trong ngày.

## Cronjob trên dashboard

Dashboard nhận diện tác vụ `backup-panel` với nhãn **Sao lưu cấu hình aaPanel**.
Sau khi triển khai backend và bộ ghi nhận `backup-monitor-run.sh`, lịch đã chọn
là 05:10 hằng ngày theo giờ máy chủ, trước đồng bộ Drive lúc 05:30:

```cron
10 5 * * * /bin/bash /root/scripts/backup-monitor-run.sh backup-panel --run
```

Dùng đường dẫn scripts đang cấu hình nếu không phải `/root/scripts`.
Bộ ghi nhận tự chuyển stdout/stderr vào `<logsDir>/backup-panel.log`, đặt
`BACKUP_PANEL_CAPTURED_LOG=1` để script không ghi log hai lần, khóa chống chạy
trùng và ghi trạng thái/thời lượng cho Cronjob. Chạy trực tiếp script vẫn tự
ghi log. Tác vụ có Chạy ngay, Xem log, Đổi giờ trong Cronjob và được di chuyển
cùng scripts/log khi đổi cấu hình. Nhật ký website/database không thêm dòng panel.

## An toàn và khác biệt so với backup gốc

- Chỉ chạy dưới root trên Linux. Yêu cầu Bash, `jq`, GNU `tar`, `sqlite3`,
  `zip`, `unzip`, `flock` và các công cụ hệ thống chuẩn. Các công cụ bắt buộc
  đã có trên máy AlmaLinux được kiểm tra; không cài thêm gói.
- Không gọi hàm `public.auto_backup_panel()`, không sửa mã Python của aaPanel.
- Dùng SQLite online `.backup`, gồm cả dữ liệu WAL đã commit, kiểm tra
  `PRAGMA quick_check`, rồi tạo SQL dump từ snapshot nhất quán từng database.
  Đây không phải giao dịch nguyên tử chung giữa nhiều database/cấu hình;
  nếu cấu hình đang được chỉnh sửa, nên chạy lại sau khi chỉnh xong.
- SQLite trên 100 MiB được lưu SQL thay cho `.db`; SQL gồm các bảng hiện có,
  không lọc riêng dòng bảng `logs` như một số bước backup gốc aaPanel.
- Bỏ cache/gói WordPress, mail, hids_data, socket và SQLite WAL/SHM tạm.
  Symlink trong nội dung cấu hình được lưu dạng link, không đọc tệp đích
  bên ngoài; symlink trên đường dẫn nguồn/lưu trữ bị từ chối.
- Kiểm tra ZIP trước khi công bố; tạo tên đích không ghi đè. ZIP/log có
  quyền `0600`, vùng staging riêng trong `/root` được dọn khi kết thúc.
- Dùng khóa layout và khóa panel-retention chung với Go để tránh chạy
  trùng, chạy đồng thời dọn panel hoặc đổi đường dẫn giữa lúc backup.
- ZIP chứa thông tin cấu hình nhạy cảm. Không lưu ở thư mục web công khai.
  Việc khôi phục cần kiểm tra ở môi trường riêng trước, không giải nén ghi đè
  trực tiếp vào aaPanel đang hoạt động.

Đã kiểm thử trên AlmaLinux với SQLite giả, bao gồm kết nối còn WAL, khôi phục
SQL, hai bản cùng giây, cache bị loại trừ, quyền file, từ chối symlink và khóa
chạy trùng. Chỉ kiểm tra đường dẫn thật bằng `--dry-run`; không tạo backup
thật, không thay đổi cron và không xóa dữ liệu backup đang có.
