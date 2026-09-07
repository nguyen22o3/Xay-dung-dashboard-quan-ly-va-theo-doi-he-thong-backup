# Tài liệu Cấu trúc Dự án (Backup Dashboard)

Tài liệu này giải thích chi tiết ý nghĩa, vai trò của từng file trong hệ thống, **chi tiết các hàm (functions) cốt lõi**, và cách mà Backend (Go) liên kết với Frontend (React).

---

## 1. Cấu trúc Frontend (React + Vite)
Nằm trong thư mục `frontend/src/`

**A. File `App.jsx` (Core/Trái tim Frontend)**
Quản lý trạng thái tổng (Global State) và chứa toàn bộ logic giao tiếp với Backend.
- `fetchBackups()`: Gọi API lấy toàn bộ danh sách lịch sử backup từ Database.
- `loadSchedules()`: Gọi API lấy danh sách các Lịch cảnh báo đang được cài đặt.
- `handleLogin()`: Gửi tài khoản/mật khẩu lên server để xác thực và nhận Token.
- `saveSchedule()` / `deleteSchedule()`: Gửi lệnh Thêm/Sửa/Xóa lịch cảnh báo.
- `createBackup()`: Thu thập dữ liệu từ Form (Nguồn, Đích, Tên) và gửi lệnh tạo bản backup lên server (dùng API `/api/backup` hoặc `/api/upload`).
- `handleDelete()`: Gửi lệnh xóa một bản backup cụ thể. Hàm này tích hợp vòng lặp để xóa sạch file đó trên tất cả các nền tảng (Server, Drive, NAS) nếu có.
- `handleSourcePickClick()` / `handleDestPickClick()`: Kích hoạt mở hộp thoại chọn thư mục/file (ưu tiên mở bằng công cụ Zenity của hệ điều hành).

**B. Các Trang (Pages)**
- `pages/Dashboard.jsx`: Trang Tổng quan. Nhận mảng dữ liệu tính toán sẵn (`stats`) từ App.jsx để render các ô số liệu (Tổng số, Thành công, Thất bại, Tỷ lệ...).
- `pages/Backups.jsx`: Trang Danh sách backup. Render dữ liệu dưới dạng bảng, thực hiện logic phân trang (`paginated`) và lọc dữ liệu (`filtered`).
- `pages/Settings.jsx`: Trang Cài đặt. Hiển thị và xử lý form nhập liệu cho "Cảnh báo thiếu Backup" và form cấu hình "Thông báo" (Telegram/Discord).

**C. Các Thành phần phụ (Components)**
- `components/Modals/CreateBackupModal.jsx`: Giao diện cửa sổ bật lên khi bấm "Tạo bản backup". Thu thập dữ liệu từ người dùng (chọn nguồn, chọn đích, đặt tên).
- `components/Modals/SourceFolderModal.jsx`: Cửa sổ duyệt cây thư mục trực tiếp trên Server, dùng làm phương án dự phòng khi công cụ hệ điều hành không hoạt động.
- `components/Layout/Sidebar.jsx`: Thanh menu bên trái giúp chuyển đổi giữa các trang.
- `components/Auth/Login.jsx`: Màn hình hiển thị yêu cầu nhập mật khẩu để bảo vệ hệ thống.

**D. Tiện ích (Utils) & CSS**
- `utils/formatters.js`: Chứa các hàm định dạng hiển thị nhỏ lẻ.
  - `formatSize(mb)`: Chuyển đổi từ Megabyte (MB) sang MB hoặc GB (VD: 1.50 GB).
  - `formatTime(isoString)`: Biến đổi chuỗi thời gian máy tính (ISO) thành định dạng ngày giờ Việt Nam chuẩn (HH:mm DD/MM/YYYY).
- `index.css`: Toàn bộ mã CSS tạo nên giao diện, màu sắc, bóng đổ và hiệu ứng của ứng dụng.

---

## 2. Cấu trúc Backend (Go)
Nằm trong thư mục `backend/`

**A. File `main.go`**
- `main()`: Hàm gốc khởi động toàn bộ server (cổng 8080), kết nối cơ sở dữ liệu SQLite, kích hoạt tiến trình chạy ngầm (cron), và định tuyến toàn bộ các API (Routing).
- `authMiddleware()`: Hàm trung gian (Middleware) dùng để chặn các yêu cầu không có Token hợp lệ, đảm bảo tính bảo mật.
- `loginHandler()`: Kiểm tra tài khoản admin và cấp phát mã JWT token.

**B. File `models.go` (Cơ sở dữ liệu)**
Định nghĩa các bảng dữ liệu bằng thư viện GORM (Object-Relational Mapping).
- `BackupRecord`: Cấu trúc bảng lưu lịch sử các lần backup thành công/thất bại.
- `BackupSchedule`: Cấu trúc bảng cài đặt lịch cảnh báo.
- `Config`: Cấu trúc bảng cài đặt cấu hình thông báo Telegram/Discord.

**C. File `backup_actions.go` (Xử lý File & Hệ thống)**
- `createBackupHandler()`: Xử lý API khi người dùng muốn backup một thư mục ĐÃ CÓ SẴN trên server cục bộ.
- `uploadBackupHandler()`: Xử lý API khi người dùng tải trực tiếp một thư mục/file TỪ MÁY TÍNH CÁ NHÂN lên trình duyệt web.
- `createBackupToDests()`: **Hàm quan trọng nhất**. Đảm nhiệm việc dùng thư viện `tar` để nén file, sau đó phân tách luồng để sao chép file đó vào thư mục Server cục bộ, hoặc gọi `rclone` để tải lên Google Drive / NAS. Sau khi hoàn tất sẽ lưu thông tin vào Database.
- `deleteBackupHandler()`: Xóa bản ghi trong Database và dùng lệnh hệ thống (hoặc `rclone delete`) để xóa file vật lý tương ứng trên các nền tảng.
- `nativePickerHandler()`: Chạy lệnh dòng lệnh `zenity` của hệ điều hành Linux để mở hộp thoại chọn thư mục chuẩn của Desktop, giúp người dùng không cần gõ tay đường dẫn.

**C. File `schedule.go` (Hệ thống chạy ngầm)**
- `startScheduleChecker()`: Khởi tạo một Goroutine (tiến trình chạy song song) lặp lại mỗi 30 giây.
- `checkBackupSchedules()`: Quét toàn bộ Lịch cảnh báo. Tính toán biểu thức Cron để ra được "Giờ đến hạn". Nếu hiện tại đã quá giờ đến hạn + thời gian ân hạn mà vẫn không thấy file mới -> Tự động sinh ra một bản ghi có trạng thái `Missed` (Thiếu file) và gửi báo động.
- `CheckAndAdvanceSchedulesForBackup()`: Hàm này được gọi ngay lập tức mỗi khi tạo thành công 1 bản backup thủ công. Nó sẽ đối chiếu Tên bản backup, nếu khớp với lịch thì đánh dấu lịch đó là "đã hoàn thành kỳ này" để không bị cảnh báo ảo.
- `scheduleHasBackup()`: Chứa câu lệnh truy vấn Database (`GORM`) để đếm xem có bản backup Thành công nào khớp tên được sinh ra trong kỳ hiện tại hay không.

**D. File `notify.go` (Hệ thống Cảnh báo)**
- `sendNotifications()`: Hàm tổng, nhận nội dung báo cáo và trạng thái. Nó tự động gán các biểu tượng cảm xúc (✅, 🚨, ⚠️) và đẩy việc gửi tin cho Telegram và Discord.
- `sendTelegram()`: Sử dụng API HTTP Request chuẩn của Telegram Bot để bắn tin nhắn vào Group chat (dựa theo Bot Token và Chat ID).
- `sendDiscord()`: Đóng gói tin nhắn thành một giao diện "Card" (Embed) có dải màu cảnh báo rõ ràng và gửi tới Discord Webhook.

---

## 3. Cách Frontend và Backend Liên kết với nhau (Flow)

Dự án hoạt động theo mô hình **Client-Server** thông qua các **RESTful API** (giao tiếp bằng định dạng JSON). Dưới đây là một ví dụ quy trình liên kết khi bạn bấm "Xóa bản backup":

1. **Người dùng thao tác (Frontend):** 
   - Bạn bấm nút thùng rác màu đỏ trên dòng dữ liệu trong `Backups.jsx`.
   - Hàm `handleDelete(item)` trong `App.jsx` được kích hoạt. Trình duyệt hiện bảng hỏi "Bạn có chắc chắn?".
2. **Gửi yêu cầu (Frontend -> Backend):** 
   - `App.jsx` dùng vòng lặp, bóc tách ID của từng nền tảng (Server, Drive) và gửi lệnh `DELETE /api/delete?id=...` đính kèm Token bảo mật.
3. **Xử lý logic (Backend):**
   - Router trong `main.go` kiểm tra Token qua `authMiddleware()`, hợp lệ thì chuyển qua `deleteBackupHandler()` trong `backup_actions.go`.
   - Lệnh truy vấn DB tìm ra đường dẫn file. Nếu file nằm ở Drive, Backend dùng `exec.Command` để chạy lệnh `rclone deletefile`. Xong thì xóa luôn bản ghi trong Database.
4. **Cập nhật giao diện (Frontend):**
   - Backend phản hồi `{"status": "success"}`.
   - `App.jsx` gọi ngay hàm `fetchBackups()` để lấy mảng dữ liệu mới nhất.
   - Giao diện tự động biến mất dòng dữ liệu đó, đồng thời `stats` (Tổng quan) trừ đi số lượng và dung lượng tương ứng.
