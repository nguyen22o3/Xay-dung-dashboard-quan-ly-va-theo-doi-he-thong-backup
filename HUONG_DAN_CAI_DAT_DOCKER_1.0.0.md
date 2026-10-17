# Hướng dẫn kéo và cài Dashboard Backup Monitor bằng Docker

Phiên bản hướng dẫn: **1.0.0**. Ngày đối chiếu mã nguồn/bộ cài: **17/10/2026**.

## 1. Mục đích và mô hình triển khai

Tài liệu hướng dẫn cài dashboard đã đóng gói trên Docker Hub xuống **máy AlmaLinux x86_64 đang cài aaPanel**. Docker và aaPanel nằm trên cùng máy; backend kết nối hệ điều hành bằng host agent, gọi là chế độ `local-agent`.

Không cần chép mã nguồn, cài Go/Node.js hoặc chạy `publish.ps1` trên máy triển khai. Không cần tự tạo `.env` chứa mật khẩu SSH cho mô hình này.

| Thành phần | Chạy ở đâu? | Vai trò |
| --- | --- | --- |
| `frontend-1.0.0` | Container Docker | Giao diện web và chuyển tiếp yêu cầu đến API. |
| `backend-1.0.0` | Container Docker | API, đăng nhập và điều phối chức năng dashboard. |
| `host-tools-1.0.0` | Chỉ dùng trong quá trình cài/cập nhật | Chứa bộ cài, helper và rclone để đưa ra AlmaLinux. |
| `backup-monitor-agent` | Dịch vụ systemd trên AlmaLinux | Cho backend thao tác với máy chủ qua Unix socket riêng. |
| Cron backup | Trên AlmaLinux | Tạo backup local, upload Drive và chạy các công việc đã được cấu hình. |

Sau cài có **hai container chạy thường trực**, không phải ba. Host-tools in script cài đặt rồi thoát là hành vi bình thường.

Luồng dữ liệu dự kiến:

```text
Website / Database / cấu hình aaPanel
                  ↓ cron backup
            Kho backup local
                  ↓ tác vụ upload
              Google Drive
                  ↓ đọc số liệu
      Dashboard: tệp, dung lượng, lịch sử
```

Tài liệu không áp dụng cho Docker Desktop Windows kết nối SSH sang máy khác. Không dùng `compose.yaml` ở gốc mã nguồn để thay Compose local-agent: bộ cài sẽ tạo đúng file triển khai cho mô hình này.

## 2. Quy tắc đọc và các thông tin cần thay

Các khối `bash` chạy trong **terminal SSH AlmaLinux**, ví dụ MobaXterm. Chỉ các khối ghi rõ `powershell` mới chạy trên Windows.

Chạy lần lượt; nếu một lệnh lỗi, xử lý trước khi tiếp tục. Không dán toàn bộ tài liệu vào terminal cùng lúc.

Trong cùng một phiên SSH, khai báo:

```bash
BM_REPO=nguyen22o3/backup-monitor
BM_VERSION=1.0.0
BM_HOST=192.168.37.130
BM_DATA_DIR=/opt/backup-monitor
```

- Đổi `BM_HOST` thành IPv4 riêng của **chính máy AlmaLinux**, không phải IP Windows. `192.168.37.130` chỉ là ví dụ.
- `BM_DATA_DIR` chứa dữ liệu vận hành ứng dụng; không phải thư mục bắt buộc chứa backup website/database.
- Mở phiên SSH mới thì cần khai báo lại các biến.
- Ba tag `host-tools-1.0.0`, `backend-1.0.0`, `frontend-1.0.0` phải được publish đầy đủ trước khi cài. Tài liệu này **không xác nhận các tag đã tồn tại trên Docker Hub**.
- `1.0`, `1.0.0`, `1.1.6` là các tag khác nhau. Retag từ `1.1.6` sang `1.0.0` chỉ đổi tên tham chiếu, không bổ sung mã nguồn mới.
- Bộ cài hiện chấp nhận `X.Y.Z`. Luôn truyền rõ `--version 1.0.0`; script được retag có thể vẫn mặc định `1.1.6` bên trong.

## 3. Chuẩn bị máy AlmaLinux

### 3.1. Bảo vệ dữ liệu đang có

Nếu máy đang có website, database hoặc lịch backup, sao lưu dữ liệu quan trọng ra nơi khác và chờ công việc đang chạy hoàn tất trước khi cài. Snapshot VMware là lớp dự phòng bổ sung, không thay thế backup độc lập và kiểm thử khôi phục.

Không xóa `/root`, `/www`, `/Backup` hoặc Docker volume để “cài sạch”. Không gỡ Nginx/MySQL/PHP của aaPanel.

Nếu chuyển từ backend chạy mã nguồn trên Windows, dừng backend đó bằng `Ctrl+C` trước khi chuyển cấu hình. Nếu từng chạy API trực tiếp trên AlmaLinux, kiểm tra:

```bash
sudo systemctl is-active backup-monitor-api
```

Chỉ khi dịch vụ này đang `active`, đã hết công việc và bạn quyết định chuyển sang Docker, dừng nó:

```bash
sudo systemctl stop backup-monitor-api
```

Không dừng nhầm `backup-monitor-agent`: bản Docker local-agent cần dịch vụ này. Chưa xóa/disable dịch vụ cũ khi bản mới chưa được nghiệm thu.

### 3.2. Kiểm tra hệ điều hành, aaPanel, IP và thời gian

```bash
cat /etc/almalinux-release
uname -m
sudo test -d /www/server/panel && echo 'Da co thu muc aaPanel'
ip -4 -br addr
ip route
df -h
timedatectl
sudo ss -ltnp 'sport = :8090'
```

Điều kiện:

- Kiến trúc `x86_64`; bộ cài hiện không hỗ trợ ARM.
- aaPanel đã cài trên đúng máy; kiểm tra Nginx/PHP/MySQL thực sự hoạt động trong aaPanel.
- Có dung lượng cho image, dữ liệu ứng dụng và kho backup. Không có mức RAM/dung lượng tối thiểu đã được nghiệm thu trong tài liệu này; cần tính theo dữ liệu và tải thực tế.
- IP thuộc máy hiện tại và thuộc dải riêng `10.x.x.x`, `172.16–31.x.x` hoặc `192.168.x.x`.
- Cổng `8090` không bị ứng dụng khác chiếm khi cài mới. Không kill tiến trình chưa xác định.
- Ngày/giờ máy chủ đúng. Cron chạy theo giờ AlmaLinux, không phải giờ Windows.

VMware NAT hoặc Bridged đều có thể dùng nếu Windows truy cập được IP máy ảo và máy ảo ra Internet. Nên dùng IP ổn định hoặc DHCP reservation hợp lệ, không tự chọn IP có thể trùng máy khác.

### 3.3. Kiểm tra mạng và DNS

```bash
getent ahostsv4 download.docker.com
getent ahostsv4 registry-1.docker.io
getent ahostsv4 www.googleapis.com
```

Nếu báo không phân giải được tên miền, xử lý DNS/mạng trước. Không tắt firewall/SELinux hoặc thay IP/gateway từ xa một cách thử nghiệm; chuẩn bị console VMware nếu phải sửa mạng.

## 4. Cài Docker Engine và Docker Compose

### 4.1. Máy đã có Docker

```bash
sudo docker info
sudo docker compose version
```

Nếu cả hai thành công, bỏ qua bước cài mới. Dùng `docker compose`, không mặc định thay bằng lệnh cũ `docker-compose`.

### 4.2. Máy chưa có Docker

Các lệnh sau theo kho RPM RHEL của Docker. AlmaLinux không được liệt kê riêng trong trang hỗ trợ RHEL của Docker; kiểm tra khả năng tương thích với bản AlmaLinux đang dùng. Nguồn: [Docker Engine trên RHEL](https://docs.docker.com/engine/install/rhel/) và [Compose plugin](https://docs.docker.com/compose/install/linux/).

```bash
sudo dnf -y install dnf-plugins-core
sudo dnf config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo
sudo dnf install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo docker info
sudo docker compose version
```

Khi được hỏi khóa GPG, đối chiếu fingerprint trong tài liệu chính thức trước khi đồng ý. Không tắt kiểm tra chữ ký. Nếu xung đột Podman/runc/gói Docker cũ, xác định dịch vụ đang sử dụng trước khi gỡ; không gỡ hàng loạt.

Không cần cài Docker Desktop trên AlmaLinux. Tiếp tục dùng `sudo docker`; không cần thêm tài khoản vào nhóm Docker chỉ để thực hiện hướng dẫn.

## 5. Kéo đủ ba image từ Docker Hub

Kiểm tra biến đã khai báo:

```bash
printf 'Repo=%s\nVersion=%s\nHost=%s\nData=%s\n' \
  "$BM_REPO" "$BM_VERSION" "$BM_HOST" "$BM_DATA_DIR"
```

Nếu cần đăng nhập Docker Hub, chạy:

```bash
sudo docker login
```

Làm theo lời nhắc Docker; nếu dùng token, không đưa token vào dòng lệnh, tài liệu hoặc ảnh chụp. Đăng nhập Docker Desktop Windows không tự đăng nhập Docker Engine trên AlmaLinux.

Tải lần lượt:

```bash
sudo docker pull "${BM_REPO}:host-tools-${BM_VERSION}"
sudo docker pull "${BM_REPO}:backend-${BM_VERSION}"
sudo docker pull "${BM_REPO}:frontend-${BM_VERSION}"
sudo docker image ls "$BM_REPO"
```

Phải tải được đủ ba tag `1.0.0`. Nếu `manifest unknown`/`not found`, dừng và kiểm tra [Tags trên Docker Hub của dự án](https://hub.docker.com/r/nguyen22o3/backup-monitor/tags). Không tự dùng `latest` hoặc trộn các phiên bản.

Nếu `unauthorized`/`denied`, kiểm tra tên repository, ngữ cảnh đăng nhập sudo và quyền đọc. Lỗi giới hạn tải cần xử lý tài khoản/quota, không đổi image nguồn tùy tiện.

## 6. Lấy và kiểm tra bộ cài

Không cần tự viết file cài đặt. Xuất script đi kèm image ra thư mục tạm để xem trước:

```bash
BM_WORK=$(mktemp -d /tmp/backup-monitor-download.XXXXXXXX)
printf 'Thu muc bo cai: %s\n' "$BM_WORK"
sudo docker run --rm "${BM_REPO}:host-tools-${BM_VERSION}" > "$BM_WORK/install-local.sh"
test -s "$BM_WORK/install-local.sh" && echo 'Script khong rong'
bash -n "$BM_WORK/install-local.sh"
less "$BM_WORK/install-local.sh"
```

Trong `less`, cuộn để đọc và nhấn `q` để thoát. `bash -n` không báo lỗi nghĩa là cú pháp đạt, chưa phải đã cài hay đã xác minh an toàn toàn bộ nội dung. Không chạy nếu file rỗng hoặc lệnh Docker xuất script bị lỗi.

Kiểm tra helper trong image:

```bash
sudo docker run --rm --entrypoint /backup-manager \
  "${BM_REPO}:host-tools-${BM_VERSION}" version
```

Kết quả mong đợi với bộ cài hiện tại:

```text
backup-manager-go-v26
```

Số `v26` là phiên bản helper, khác tag phát hành Docker `1.0.0`. Nếu không đúng, không bỏ qua kiểm tra của bộ cài.

Chỉ pull image hoặc chạy container host-tools chưa cài dashboard. Bộ cài phải chạy trên host ở bước sau để thiết lập systemd, cấu hình và quyền cần thiết. Không thay bằng container `--privileged`, không mount `/` hoặc Docker socket để ép cài.

## 7. Cài dashboard

### 7.1. Chọn thư mục ứng dụng

Ví dụ dùng `/opt/backup-monitor`. Bộ cài tổ chức `config`, `scripts`, `logs`, `recovery`, `tmp` và `deployment` dưới thư mục này.

Phân biệt:

| Đường dẫn | Ý nghĩa |
| --- | --- |
| `/opt/backup-monitor` | Thư mục ứng dụng trong ví dụ. |
| `/www/backup` | Kho backup local mặc định của cấu hình mới. |
| `/Backup` | Ví dụ kho backup local khác nếu bạn chọn; không tự là mặc định. |
| `gdrive:Backup` | Remote và thư mục trên Google Drive. |

Không để nguồn website/MySQL data trùng kho backup hoặc scripts/logs. Đường dẫn mới phải an toàn, không symlink, thư mục cha do root quản lý. **Không tạo sẵn cả cây thư mục ứng dụng chỉ để vượt kiểm tra của bộ cài**.

Nếu đã cài v26 vào một data-dir, dùng lại đúng data-dir đó. V26 hiện không hỗ trợ đổi cả thư mục ứng dụng đã đăng ký bằng cách nhập một đường dẫn khác trong lần nâng cấp. Lần chuyển dữ liệu ứng dụng đầu tiên giữ bản gốc; các archive backup vẫn ở kho hiện có, không tự chuyển theo. Không xóa toàn bộ `/root` sau cài.

### 7.2. Chạy bộ cài trên AlmaLinux

```bash
sudo bash "$BM_WORK/install-local.sh" \
  --host "$BM_HOST" \
  --version "$BM_VERSION" \
  --data-dir "$BM_DATA_DIR"
```

Khi hỏi:

```text
Install/update using this application folder? Type yes:
```

Đọc kế hoạch đường dẫn và chỉ nhập `yes` nếu đúng máy/đúng dữ liệu. Không dán lệnh khác vào lời nhắc này. Nếu bỏ `--data-dir`, có thể được hỏi `Application folder [/opt/backup-monitor]:`; Enter nghĩa là dùng mặc định.

Bộ cài có thể cài gói host, thiết lập SELinux policy, tạm dừng cron/agent trong bước chuyển rồi khởi động lại. Giữ phiên SSH mở, thực hiện ngoài thời điểm job quan trọng.

### 7.3. Tạo tài khoản quản trị lần đầu

```text
Administrator username:
Administrator password (12-72 bytes):
Repeat password:
```

- Username: 3–64 ký tự; bắt đầu bằng chữ/số, các ký tự còn lại là chữ/số, `_`, `.` hoặc `-`.
- Password: 12–72 byte; dùng mật khẩu riêng đủ mạnh. Unicode có thể chiếm nhiều byte.
- Không hiện ký tự khi nhập mật khẩu là bình thường.
- Đây là tài khoản dashboard, không phải SSH, aaPanel hay MySQL.
- Bản cài đã có tài khoản sẽ giữ tài khoản cũ, không tự đặt lại mật khẩu.

Không bật `bash -x` hoặc quay/chụp màn hình chứa bí mật. Bộ cài tạo JWT và thông tin xác thực agent riêng; không cần mật khẩu mẫu dùng chung.

### 7.4. Kết quả cài đặt

Khi hoàn tất, có thông báo dạng:

```text
Dashboard: http://192.168.37.130:8090
Application folder: /opt/backup-monitor
```

Bộ cài **không cài LEMP thay aaPanel, không tự kết nối Drive, không tạo lịch backup mới và không chạy backup**. Cấu hình/lịch cũ có thể được giữ/chuyển khi nâng cấp; không nhầm điều đó với lịch mới được tạo tự động.

## 8. Kiểm tra sau cài và mở dashboard

Trên AlmaLinux:

```bash
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" ps
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" config --images
sudo systemctl status backup-monitor-agent --no-pager
sudo systemctl is-active crond
curl --connect-timeout 5 --max-time 15 -fsS "http://${BM_HOST}:8090/api/health"
```

Mong đợi:

- Backend `healthy`; frontend đang chạy.
- Image của service là `backend-1.0.0` và `frontend-1.0.0` trong repository đúng.
- Agent và `crond` active.
- API health phản hồi thành công. Health chưa chứng minh Drive/MySQL đã cấu hình đúng.

Kiểm tra kết nối backend → host agent:

```bash
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" \
  run --rm --no-deps -T backend check-host
```

Mong đợi `Local host agent connected`. Không cần in/xem agent token để kiểm tra.

Mở trình duyệt Windows đến **`http://<IP-AlmaLinux>:8090`**, ví dụ `http://192.168.37.130:8090`, và đăng nhập bằng tài khoản vừa tạo.

Không dùng `localhost:5173`: đó là frontend phát triển. `localhost:8090` trên Windows không phải máy ảo nếu chưa có tunnel/forwarding riêng. Không thêm đường dẫn bảo mật aaPanel vào URL dashboard.

Nếu không truy cập được, kiểm tra từ PowerShell Windows:

```powershell
Test-NetConnection 192.168.37.130 -Port 8090
```

Đổi IP theo máy thật. Compose bind vào IP riêng đã chọn; không tắt firewall/SELinux để sửa lỗi. Bộ cài không tự thêm quy tắc public. Cần đối chiếu cả port publishing của Docker và firewall host; không giả định quy tắc thông thường luôn kiểm soát được traffic container. Nguồn: [Docker và firewall](https://docs.docker.com/engine/network/packet-filtering-firewalls/).

HTTP chỉ phù hợp mạng lab tin cậy. Khi triển khai thực tế cần HTTPS/reverse proxy và cấu hình origin phù hợp. Không mở API `8080`, socket agent hoặc Docker daemon ra Internet.

## 9. Chuẩn bị backup database

Database được xuất bằng `mysqldump`; **không tar/copy thư mục dữ liệu MySQL đang chạy**. Thư mục data được chọn trong giao diện giúp xác định database, không thay logical dump.

Kiểm tra client:

```bash
command -v mysqldump
sudo ls -l /www/server/mysql/bin/mysqldump
```

Nếu aaPanel có client nhưng script không tìm thấy trong PATH, cần xử lý PATH/client trước khi lên lịch; không cài thêm một MySQL server khác hoặc ghi đè symlink tùy tiện.

Với data-dir trong tài liệu, file kết nối là:

```text
/opt/backup-monitor/config/mysql.cnf
```

Kiểm tra file trước; không in nội dung có mật khẩu ra màn hình để gửi hỗ trợ:

```bash
sudo ls -l "$BM_DATA_DIR/config/mysql.cnf"
```

Nếu chưa có, tạo file riêng tư và mở editor:

```bash
sudo touch "$BM_DATA_DIR/config/mysql.cnf"
sudo chown root:root "$BM_DATA_DIR/config/mysql.cnf"
sudo chmod 600 "$BM_DATA_DIR/config/mysql.cnf"
sudo vi "$BM_DATA_DIR/config/mysql.cnf"
```

Mẫu nội dung, thay bằng tài khoản thực sự được phép dump:

```ini
[client]
user=TAI_KHOAN_BACKUP_DATABASE
password="MAT_KHAU_DATABASE_THUC_TE"
host=127.0.0.1
port=3306
```

Trong `vi`: `i` để sửa, Esc rồi `:wq` Enter để lưu. Nếu file đã có, cập nhật đúng phần cần thiết, không xóa nội dung cũ hoặc thêm lặp `[client]`. Nếu dùng socket, cấu hình socket thực tế thay vì đoán địa chỉ/port. Mật khẩu có dấu nháy/backslash cần cú pháp MySQL options đúng.

Có thể thử kết nối bằng client aaPanel sau khi xác nhận đường dẫn tồn tại:

```bash
sudo /www/server/mysql/bin/mysql \
  --defaults-extra-file="$BM_DATA_DIR/config/mysql.cnf" \
  -e 'SELECT 1;'
```

Kết nối đạt chưa chứng minh đủ quyền dump routines/events/triggers. Nghiệm thu bằng backup thử trên dữ liệu được phép. Không đưa file này lên Git hoặc Docker Hub.

## 10. Kết nối Google Drive

### 10.1. Chuẩn bị quyền truy cập

Trong Google Cloud, tạo/chọn project, bật Google Drive API và tạo OAuth client phù hợp luồng desktop của rclone. Lưu riêng Client ID/Client secret; API key không thay thế OAuth client. Làm theo [hướng dẫn Drive chính thức của rclone](https://rclone.org/drive/#making-your-own-client-id).

Với server không có trình duyệt, dùng rclone trên Windows để cấp quyền và nhận JSON token. Nên dùng phiên bản tương thích với rclone trên máy chủ. Nguồn tải: [rclone downloads](https://rclone.org/downloads/).

Ví dụ PowerShell trong thư mục có `rclone.exe`:

```powershell
.\rclone.exe authorize drive "CLIENT_ID_THUC_TE" "CLIENT_SECRET_THUC_TE"
```

Đăng nhập đúng tài khoản Google trong trình duyệt và cấp quyền. Sao chép nguyên JSON token, không kèm lời giải thích. Client secret và token là bí mật; lệnh có thể lưu vào lịch sử terminal, chỉ thực hiện trên máy tin cậy và bảo vệ lịch sử đó. Nếu rclone in một lệnh authorize cho luồng headless cụ thể, dùng đúng lệnh nó cung cấp. Nguồn: [rclone authorize](https://rclone.org/commands/rclone_authorize/).

OAuth External ở trạng thái Testing có thể cấp refresh token hết hạn sau 7 ngày với phạm vi Drive. Cần xử lý trạng thái xuất bản/xác minh phù hợp cho vận hành lâu dài; không coi token luôn tồn tại vĩnh viễn. Nguồn: [Google OAuth token expiration](https://developers.google.com/identity/protocols/oauth2#expiration).

### 10.2. Lưu trên dashboard

Vào **Cron → Kết nối Google Drive**:

1. Nhập tên kết nối, ví dụ `gdrive` nếu chưa tồn tại.
2. Nhập Client ID và Client secret cùng bộ đã dùng cấp quyền.
3. Dán nguyên JSON token.
4. Kiểm tra và lưu kết nối. Nếu có cảnh báo HTTP, chỉ tiếp tục trong LAN tin cậy đã đánh giá.

Không xóa remote đang được cron dùng chỉ vì form báo trùng tên. Với remote cũ cần cấp lại quyền, xem mục xử lý lỗi bên dưới.

Kiểm tra trên AlmaLinux, đổi `gdrive` nếu dùng tên khác:

```bash
sudo /usr/bin/rclone listremotes \
  --config "$BM_DATA_DIR/config/rclone/rclone.conf"
sudo /usr/bin/rclone about gdrive: \
  --config "$BM_DATA_DIR/config/rclone/rclone.conf" \
  --timeout 15s --contimeout 5s
```

Thư mục `Backup` có thể chưa tồn tại ở lần đầu; tạo/chọn đúng qua giao diện. Lưu kết nối chưa tự upload hoặc tạo lịch.

## 11. Cấu hình nguồn, kho lưu và cron hằng ngày

Vào **Cron → Thêm tác vụ**, chọn công việc và xác nhận nguồn thực tế.

| Tác vụ | Nguồn ví dụ | Đích |
| --- | --- | --- |
| Sao lưu website | `/www/wwwroot`, chọn đủ website cần bảo vệ | Kho local đã chọn. |
| Sao lưu database | `/www/server/data`, chọn database theo cấu hình | Logical dump trong kho local. |
| Sao lưu cấu hình aaPanel | `/www/server/panel` | ZIP cấu hình panel; không thay backup website/database. |
| Đồng bộ Google Drive | Đúng kho backup local phía trên | Remote `gdrive`, thư mục `Backup`. |

Đường dẫn local mới thường có cấu trúc:

```text
/www/backup/YYYY-MM-DD/site/<website>/...tar.gz
/www/backup/YYYY-MM-DD/database/mysql/crontab_backup/<database>/...sql.gz
/www/backup/YYYY-MM-DD/panel/...zip
```

Nếu kho thật là `/Backup`, giữ đúng kho đó ở các tác vụ cần dùng; không đổi theo ví dụ một cách máy móc. Các cron có cấu hình riêng: đổi đích của cron backup chưa chắc đổi nguồn của cron upload.

Lịch ví dụ cho lab, giờ máy chủ:

| Giờ | Công việc |
| --- | --- |
| 04:30 | Backup database. |
| 05:00 | Backup website. |
| 05:20 | Backup cấu hình aaPanel. |
| 06:00 | Upload kho backup local lên Drive. |
| 07:00 | Kiểm tra số thư mục backup trên Drive nếu đã cấu hình. |

Đây là ví dụ, **không phải chuỗi phụ thuộc bắt buộc**. Nếu backup chưa xong lúc 06:00, upload vẫn có thể chạy thiếu dữ liệu hôm nay. Đo thời gian thực tế, đặt khoảng cách phù hợp và kiểm tra log. Không tạo lịch trùng nhau trong aaPanel và dashboard nếu không chủ ý.

Trước khi bật lịch chính thức, chạy thử từng bước: database → website → aaPanel → xác nhận đủ file local → upload → xác nhận file trên Drive. Muốn nghiệm thu ba website, đối chiếu từng website và database tương ứng, không chỉ đếm thư mục ngày.

### Chính sách lưu hiện tại: 14, không phải 15

Mã nguồn hiện khai báo `RetentionDays = 14`; script Drive mới giữ 14 ngày backup hợp lệ có archive. Đây không phải cam kết đủ 14 ngày lịch liên tiếp khi có ngày bị bỏ lỡ. Local và Drive có cách xác định dữ liệu quá hạn riêng; không suy ra chỉ từ số folder.

Yêu cầu giữ 15 ngày cần sửa/kiểm thử retention và publish bản tương ứng. Đặt ngưỡng cảnh báo bằng 15 không thay retention. Backup/upload có thể kèm dọn bản quá hạn; dùng kho thử hoặc backup độc lập trước nghiệm thu. Không chạy upload trên dữ liệu quan trọng chỉ để thử giao diện.

## 12. Giám sát và nghiệm thu

Trên dashboard kiểm tra:

- Tổng quan: số tệp, tổng dung lượng local/Drive, dung lượng theo ngày/loại và hoạt động gần đây.
- Máy chủ: tình trạng máy, website và backup local.
- Google Drive/Bản sao lưu: tệp thực tế và dung lượng.
- Cron: lịch bật, lần chạy, kết quả và log.
- Nhật ký hoạt động: thời điểm bắt đầu, kết quả và thời gian thực hiện.
- Cài đặt: thư mục giám sát và kênh cảnh báo cần dùng.

Giao diện image cũ có thể khác source local. Sửa source chưa có nghĩa image trên Docker Hub đã chứa sửa đổi.

Lệnh chỉ đọc để đối chiếu, thay đúng kho local/remote:

```bash
sudo du -sh /www/backup
sudo /usr/bin/rclone lsf gdrive:Backup --dirs-only \
  --config "$BM_DATA_DIR/config/rclone/rclone.conf"
sudo /usr/bin/rclone size gdrive:Backup \
  --config "$BM_DATA_DIR/config/rclone/rclone.conf"
sudo crontab -l
```

Kiểm tra chi tiết một ngày trên Drive:

```bash
BM_DAY=$(date +%F)
sudo /usr/bin/rclone lsf "gdrive:Backup/$BM_DAY" -R --files-only --format ps \
  --config "$BM_DATA_DIR/config/rclone/rclone.conf"
```

`rclone size` có thể lâu với kho lớn; không chạy lặp liên tục. Nguồn: [rclone lsf](https://rclone.org/commands/rclone_lsf/), [rclone size](https://rclone.org/commands/rclone_size/).

Có folder ngày hoặc tổng dung lượng gần đúng chưa chứng minh backup đủ/khôi phục được. Tác vụ kiểm tra số thư mục hiện chủ yếu đếm folder ngày, không xác nhận đủ từng website/database. Kiểm thử khôi phục trong môi trường riêng, không ghi đè website/database đang phục vụ.

## 13. Vận hành và cập nhật

### 13.1. Xem log và khởi động lại

```bash
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" logs --tail=100 backend
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" logs --tail=100 frontend
sudo journalctl -u backup-monitor-agent -n 100 --no-pager
```

Khi đã đánh giá ảnh hưởng và chờ job quan trọng hoàn tất, có thể restart dashboard:

```bash
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" restart backend frontend
```

Restart không tải image mới. Dừng container không tắt cron host; restart agent/container không phải lệnh hủy tác vụ đã tách ra chạy. Không dừng `crond` toàn máy làm cách vận hành thông thường.

### 13.2. Cập nhật giao diện/API nhưng giữ tag `1.0.0`

Chỉ sau khi tác giả đã push nội dung mới cùng tag, xác nhận Compose đang dùng đúng `1.0.0`, sao lưu dữ liệu và ghi nhận image ID/digest đang dùng:

```bash
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" config --images
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" images
```

Nếu chỉ đổi frontend/backend, không đổi helper/agent/cấu trúc dữ liệu, chờ hết công việc quan trọng rồi cập nhật:

```bash
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" \
  up -d --pull always --wait --wait-timeout 180
sudo docker compose -f "$BM_DATA_DIR/deployment/compose.yaml" ps
```

Compose tải image và tạo lại container khi cần, giữ volume gắn hiện có. Kiểm tra lại mục 8 và `Ctrl+F5` trình duyệt. Nguồn: [Docker Compose up](https://docs.docker.com/reference/cli/docker/compose/up/).

### 13.3. Cập nhật cả helper/host-tools hoặc sang release khác

Lấy **bộ cài từ image host-tools của release đích** theo mục 5–6, giữ đúng host/data-dir và chạy lại mục 7. Nếu giữ tag, dùng `BM_VERSION=1.0.0`; nếu sang bản mới, đặt phiên bản thực tế đã publish đủ cả ba image.

Chỉ push/pull host-tools không tự thay helper trên AlmaLinux. Chỉ cập nhật hai container chưa cập nhật host agent/helper. Script lưu ở `<data-dir>/deployment/install-local.sh` chỉ nên dùng cho release đích khi đã xác nhận tương thích; không mặc định script cũ xử lý được mọi nâng cấp.

Nếu chuyển từ `1.1.6` sang `1.0.0`, phải biết đây là retag cùng mã hay hạ về mã cũ. Không rollback qua tên tag khi dữ liệu/helper đã đổi mà chưa kiểm tra tương thích. Không xóa tag cũ trên Hub trước khi máy mới đã chạy ổn và không còn deployment cần pull nó.

**Không dùng `docker compose down -v`, `docker volume rm` hay `docker system prune --volumes` để cập nhật.**

## 14. Những dữ liệu phải bảo vệ

| Vị trí mặc định | Nội dung |
| --- | --- |
| `/opt/backup-monitor/config/` | Cấu hình, rclone token, MySQL options và metadata. |
| `/opt/backup-monitor/scripts/`, `logs/`, `recovery/` | Script, lịch sử và dữ liệu phục hồi. |
| `/opt/backup-monitor/deployment/` | Compose và script cập nhật. |
| Docker volume `backup-monitor-local_app-data` | Tài khoản, JWT và trạng thái API. |
| `/etc/backup-monitor/installation.json` | Locator vị trí cài đặt. |
| `/var/lib/backup-monitor-agent/secrets/` | Thông tin xác thực agent, có quyền rất nhạy cảm. |
| `/etc/systemd/system/backup-monitor-agent.service` | Dịch vụ host agent. |
| `/usr/local/libexec/backup-monitor/` | Helper và bản dự phòng khi cập nhật. |
| Root crontab và kho backup thực tế | Lịch chạy và dữ liệu backup local/Drive. |

Nếu đã tùy chỉnh project/volume, dùng `sudo docker volume ls` và mount thực tế để xác định; không đoán tên volume. Chỉ sao lưu data-dir mà bỏ qua Docker volume là thiếu dữ liệu.

Trước cập nhật quan trọng, có thể chờ hết job rồi tắt VM an toàn và sao chép đủ máy ảo như một lớp dự phòng, đồng thời giữ backup website/SQL độc lập. Bản sao chứa token/mật khẩu phải được mã hóa hoặc bảo vệ quyền, không upload lên repository public. Không đưa hướng dẫn xóa/khôi phục đè hàng loạt vào thao tác cập nhật thường ngày.

## 15. Xử lý lỗi thường gặp

| Hiện tượng | Kiểm tra và hướng xử lý |
| --- | --- |
| `manifest unknown` / `not found` | Kiểm tra đủ ba tag đúng `1.0.0`; không tự đổi sang `1.0` hoặc `latest`. |
| `Invalid release` | Dùng phiên bản ba phần `X.Y.Z`, truyền rõ `--version 1.0.0`. |
| `Cannot connect to Docker daemon` | Kiểm tra `sudo systemctl status docker --no-pager`, `sudo docker info`. |
| Không có `docker compose` | Cài plugin từ kho Docker phù hợp. |
| `Install aaPanel on this host first` | Đang chạy đúng máy không, aaPanel đã ở `/www/server/panel` chưa? |
| IP không thuộc host | Lấy IP bằng `ip -4 -br addr`, không dùng IP Windows/máy ảo cũ. |
| Cổng `8090` bị chiếm | Đọc `sudo ss -ltnp 'sport = :8090'`; không kill tiến trình chưa xác định. |
| `Host UID/GID 10001 is already allocated` | Kiểm tra `getent passwd 10001`, `getent group 10001`; không xóa/đổi UID tài khoản đang dùng để ép cài. |
| Thư mục đích tồn tại / không an toàn | Kiểm tra bản cài cũ, symlink, quyền thư mục; không xóa data-dir hoặc locator để vượt kiểm tra. |
| Backend unhealthy / agent không chạy | Xem log backend, journal agent và `check-host`; kiểm tra quyền/SELinux thay vì tắt bảo vệ. |
| Máy chủ mở được nhưng Windows không mở được | Kiểm tra đúng IP, VMware, port và firewall/Docker publishing. |
| Vẫn thấy giao diện cũ | Kiểm tra IP/cổng, tag/digest đang chạy; xác nhận đã publish/build mã mới rồi `Ctrl+F5`. |
| Chưa thấy backup sau cài | Installer không chạy backup mới; kiểm tra nguồn, lịch, kho local, Drive và log từng bước. |
| Backup DB lỗi | Kiểm tra `mysqldump`, PATH, `mysql.cnf` root-owned quyền `600`, tài khoản/port/socket và quyền dump. |

### 15.1. Drive báo `invalid_client`

Đối chiếu Client ID/Client secret cùng một OAuth client. Client secret sai không được sửa bằng cách đổi tag Docker. Không gửi secret/token vào chat để nhờ kiểm tra.

### 15.2. Drive báo `invalid_grant` / token hết hiệu lực

Chờ job Drive hoàn tất, xác nhận file cấu hình đúng rồi cấp lại quyền cho remote đang dùng:

```bash
sudo /usr/bin/rclone config reconnect gdrive: \
  --config "$BM_DATA_DIR/config/rclone/rclone.conf"
```

Làm theo lời nhắc; dùng luồng headless trên Windows nếu server không có trình duyệt. Chỉ đồng ý đổi token cho đúng remote/tài khoản cần cập nhật. Sau đó thử `about` như mục 10 rồi mới chạy upload. Log thất bại cũ không tự đổi thành thành công sau reconnect. Nguồn: [rclone reconnect](https://rclone.org/commands/rclone_config_reconnect/) và [headless setup](https://rclone.org/remote_setup/).

`didn't find section ... gdrive` thường là sai tên remote hoặc sai file config. Không in toàn bộ `rclone.conf` để gửi hỗ trợ.

### 15.3. Cron chưa chạy hoặc không import được

```bash
timedatectl
sudo systemctl status crond --no-pager
sudo crontab -l
```

Kiểm tra lịch có bật, đúng múi giờ, máy ảo có chạy đúng giờ, script tồn tại và log có lỗi không. Cron không tự bảo đảm chạy bù mọi lịch đã lỡ khi máy tắt.

Nếu import lỗi và root **chưa có crontab** (`no crontab for root`), có thể mở `sudo crontab -e`, thêm một dòng chú thích `# Backup Monitor schedules`, lưu và thử lại. Nếu có lịch sẵn hoặc lỗi khác, không thay toàn bộ crontab. Không dùng `crontab -r`.

### 15.4. Quên mật khẩu dashboard

Mật khẩu aaPanel và `bt 5` không đổi mật khẩu dashboard. `setup-admin` hiện dùng cho khởi tạo khi chưa có tài khoản, không phải reset tài khoản đã tồn tại. Không xóa volume/file tài khoản để thử; dùng quy trình khôi phục được xác nhận hoặc bản sao đã bảo vệ.

## 16. Checklist hoàn tất

- [ ] Đúng AlmaLinux x86_64, aaPanel/LEMP đã chạy, IP và thời gian đúng.
- [ ] Docker Engine và Compose plugin hoạt động.
- [ ] Pull thành công đủ ba image `1.0.0`; helper đúng v26.
- [ ] Đã đọc bộ cài và chọn đúng data-dir; không nhầm mô hình SSH/Windows.
- [ ] Backend healthy, frontend chạy, agent/crond active; `check-host` đạt.
- [ ] Đăng nhập được qua IP AlmaLinux cổng `8090` bằng tài khoản riêng.
- [ ] MySQL options/client và quyền dump đã được thử thật.
- [ ] Drive kết nối đúng tài khoản, hiểu vấn đề token dài hạn.
- [ ] Đủ nguồn ba website/database cần bảo vệ và đúng kho local/Drive.
- [ ] Lịch không trùng, có khoảng cách phù hợp; đã chạy thử đúng thứ tự.
- [ ] Xác nhận từng file local/Drive, không chỉ nhìn số folder hoặc tổng dung lượng.
- [ ] Hiểu retention hiện là 14; yêu cầu 15 ngày chưa tự được đáp ứng.
- [ ] Đã nghiệm thu cảnh báo/khôi phục trên môi trường riêng và có backup độc lập.
- [ ] Đã bảo vệ cấu hình, Docker volume và thông tin xác thực trước cập nhật.

**Phạm vi xác minh:** tài liệu đối chiếu mã nguồn/bộ cài và nguồn chính thức. Việc soạn tài liệu không cài lên máy chủ, không publish image, không tạo cron, không upload/xóa backup và không xác nhận các tag đã phát hành. Chỉ coi triển khai thành công sau khi các bước kiểm tra thực tế ở trên đạt.
