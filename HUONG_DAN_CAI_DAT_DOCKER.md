# Hướng dẫn tải từ Docker Hub và cài đặt Backup Monitor

Tài liệu này dành cho máy **AlmaLinux x86_64 đã cài aaPanel**. Dashboard, Docker và aaPanel chạy trên cùng một máy. Bản được dùng trong ví dụ là `1.1.6`, gồm ba image đã phát hành trên Docker Hub:

- `nguyen22o3/backup-monitor:host-tools-1.1.6`: bộ cài và chương trình kết nối với hệ điều hành;
- `nguyen22o3/backup-monitor:backend-1.1.6`: API;
- `nguyen22o3/backup-monitor:frontend-1.1.6`: giao diện web.

Đây là cách cài đặt **local-agent**. Bộ cài tự tải ba image, thiết lập dịch vụ host agent, tạo cấu hình Docker Compose và hỏi tên đăng nhập/mật khẩu quản trị ở lần đầu. Không cần chép mã nguồn dự án hoặc tạo file `.env` trên máy chủ.

## 1. Chuẩn bị máy chủ

Đăng nhập vào AlmaLinux bằng SSH với tài khoản có quyền `sudo`. Kiểm tra aaPanel, kiến trúc máy và địa chỉ IPv4:

```bash
uname -m
test -d /www/server/panel && echo 'aaPanel đã được cài'
ip -4 -br addr
```

Máy phải là `x86_64`. Ghi lại **địa chỉ IPv4 riêng của chính máy AlmaLinux**, ví dụ `192.168.37.130`. Bộ cài chỉ chấp nhận dải riêng `10.x.x.x`, `172.16–31.x.x` hoặc `192.168.x.x`. Bảo đảm máy truy cập được Docker Hub và cổng `8090` chưa bị ứng dụng khác sử dụng.

## 2. Cài Docker Engine và Docker Compose

Nếu đã chạy được `sudo docker info` và `sudo docker compose version`, bỏ qua bước này. Trên AlmaLinux tương thích RHEL, có thể dùng kho RPM của Docker như sau:

```bash
sudo dnf -y install dnf-plugins-core
sudo dnf config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo
sudo dnf install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo docker info
sudo docker compose version
```

Nếu DNF báo xung đột gói, kiểm tra các gói Docker/Podman đang dùng trước khi gỡ; không gỡ hàng loạt trên máy đang chạy dịch vụ khác. Docker công bố [hướng dẫn cài Engine cho RHEL](https://docs.docker.com/engine/install/rhel/) và [hướng dẫn Compose plugin](https://docs.docker.com/compose/install/linux/). AlmaLinux không được nêu riêng trong danh sách hệ điều hành được Docker hỗ trợ, nên cần kiểm tra kết quả cài trên phiên bản AlmaLinux đang sử dụng.

## 3. Kéo bộ cài từ Docker Hub

Tạo thư mục làm việc, tải image bộ cài và xuất script ra file để xem trước:

```bash
mkdir -p backup-monitor-install
cd backup-monitor-install
sudo docker pull nguyen22o3/backup-monitor:host-tools-1.1.6
sudo docker run --rm nguyen22o3/backup-monitor:host-tools-1.1.6 > install-local.sh
less install-local.sh
```

Sau khi đọc script, thay `192.168.37.130` bằng IPv4 riêng của **máy AlmaLinux hiện tại** và chạy:

```bash
sudo bash ./install-local.sh --host 192.168.37.130 --version 1.1.6
```

Bộ cài sẽ kéo tiếp image `backend-1.1.6` và `frontend-1.1.6`, cài các gói cần thiết trên AlmaLinux, cấu hình dịch vụ `backup-monitor-agent`, rồi khởi động hai container. Khi được hỏi, nhập **tên quản trị và mật khẩu mới** (mật khẩu 12–72 byte). Lần cập nhật sau tài khoản và dữ liệu hiện có được giữ lại.

Không ngắt phiên SSH khi bộ cài đang chạy. Khi hoàn tất, màn hình sẽ in địa chỉ Dashboard, ví dụ:

```text
Dashboard: http://192.168.37.130:8090
```

Mở địa chỉ đó trên trình duyệt của máy có thể kết nối đến AlmaLinux, rồi đăng nhập bằng tài khoản vừa tạo. Nếu dùng máy ảo VMware, kiểm tra chế độ mạng NAT/Bridged và tường lửa khi trình duyệt không truy cập được. Địa chỉ HTTP này chỉ dùng trong mạng tin cậy; nếu công bố ra Internet, cần đặt sau reverse proxy HTTPS.

## 4. Kiểm tra sau cài đặt

```bash
sudo docker compose -f /opt/backup-monitor-local/compose.yaml ps
sudo systemctl status backup-monitor-agent --no-pager
curl -fsS http://192.168.37.130:8090/api/health
```

Hai container cần ở trạng thái chạy, backend cần đạt `healthy`, host agent cần `active (running)` và API health cần phản hồi thành công. Khi cần xem lỗi:

```bash
sudo docker compose -f /opt/backup-monitor-local/compose.yaml logs --tail=100 backend
sudo docker compose -f /opt/backup-monitor-local/compose.yaml logs --tail=100 frontend
sudo journalctl -u backup-monitor-agent -n 100 --no-pager
```

Sau khi đăng nhập, vào mục **Cron** để kết nối tài khoản Google Drive của bạn. Bộ cài không tự tạo lịch backup hoặc chạy backup. Chỉ cấu hình lịch sau khi đã kiểm tra kết nối và nơi lưu dữ liệu.

## 5. Cập nhật lên bản mới

Chỉ thay `X.Y.Z` bằng phiên bản mà cả ba tag `host-tools`, `backend` và `frontend` đã được phát hành. Dùng lại script được bộ cài lưu trên máy:

```bash
sudo bash /opt/backup-monitor-local/install-local.sh --host 192.168.37.130 --version X.Y.Z
```

Bộ cài lưu bản sao cấu hình/dịch vụ trước khi thay thế và giữ Docker volume chứa tài khoản, JWT secret và trạng thái ứng dụng. Nên sao lưu dữ liệu quan trọng trước khi cập nhật. Không chạy `docker compose down -v`: tùy chọn `-v` sẽ xóa volume dữ liệu.

## Khi cài đặt dừng giữa chừng

| Thông báo | Cách kiểm tra |
| --- | --- |
| `Install aaPanel on this host first` | Cài aaPanel trên đúng máy AlmaLinux trước khi chạy bộ cài. |
| `Port 8090 is already in use` | Xác định ứng dụng đang dùng cổng bằng `sudo ss -ltnp 'sport = :8090'`; giải quyết xung đột rồi chạy lại. |
| `--host must be an address of this AlmaLinux host` | Lấy IPv4 thực từ `ip -4 -br addr` và nhập lại `--host`. |
| `Host UID/GID 10001 is already allocated` | Kiểm tra UID/GID `10001` trên máy; bộ cài cố ý dừng để tránh cấp quyền host agent cho tài khoản khác. |
| Import Cron trả `Không hoàn tất được lệnh hệ thống: crontab` | Chạy `sudo crontab -l` trên AlmaLinux. Nếu kết quả là `no crontab for root`, chạy `sudo crontab -e`, thêm một dòng chú thích như `# Backup Monitor schedules`, lưu rồi import lại. Nếu có lịch hoặc báo lỗi khác, không ghi đè crontab; kiểm tra `cronie` và quyền chạy lệnh. |
| Không mở được trang web | Kiểm tra `docker compose ps`, mạng máy ảo và quy tắc tường lửa cho cổng `8090` trong mạng tin cậy. |

Đường dẫn sau khi cài: Compose và script cập nhật ở `/opt/backup-monitor-local/`; dữ liệu ứng dụng nằm trong Docker volume của dự án `backup-monitor-local`; socket và khóa kết nối host agent ở `/var/lib/backup-monitor-agent/`.
