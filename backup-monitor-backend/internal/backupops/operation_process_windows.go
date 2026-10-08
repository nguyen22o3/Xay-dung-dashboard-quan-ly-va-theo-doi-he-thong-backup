//go:build windows

package backupops

import "errors"

func validateTaskProcess(TaskState, string) (bool, error) {
	return false, errors.New("Quản lý tiến trình chỉ hỗ trợ Linux")
}
func cancelTaskProcess(TaskState, string) error {
	return errors.New("Quản lý tiến trình chỉ hỗ trợ Linux")
}
func restoreFreeBytes(string) (uint64, error) {
	return 0, errors.New("Khôi phục sản xuất chỉ hỗ trợ Linux")
}
