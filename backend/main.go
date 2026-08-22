package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Khai báo cấu trúc dữ liệu trả về cho Frontend
type APIResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func main() {
	// 1. Tạo một đường dẫn API tên là /api/health để kiểm tra trạng thái
	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		// Báo cho trình duyệt biết dữ liệu trả về là chuẩn JSON
		w.Header().Set("Content-Type", "application/json")

		// Khởi tạo dữ liệu
		response := APIResponse{
			Status:  "success",
			Message: "Backend Go đã khởi chạy thành công! Sẵn sàng nhận dữ liệu backup.",
		}

		// Chuyển đổi dữ liệu sang JSON và gửi về cho người dùng
		json.NewEncoder(w).Encode(response)
	})

	// 2. Khởi động Web Server tại cổng 8080
	port := ":8080"
	fmt.Println("🚀 Backend Go đang chạy tại địa chỉ: http://localhost" + port)
	
	err := http.ListenAndServe(port, nil)
	if err != nil {
		fmt.Println("Lỗi khi khởi chạy server:", err)
	}
}